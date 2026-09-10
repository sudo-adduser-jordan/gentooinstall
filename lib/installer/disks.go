package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gentooinstall/lib/disklayout"
)

// resolveID resolves a layout id, returning a canonicalized device path.
func resolveID(ctx *Context, id string) (string, error) {
	dev, err := ctx.Resolver.ResolveDevice(id)
	if err != nil {
		return "", fmt.Errorf("could not resolve device with id=%s: %w", id, err)
	}
	return dev, nil
}

// describeDevices renders "dev (id), dev2 (id2)" for log messages.
func describeDevices(devs []string, ids []string) string {
	var parts []string
	for index, dev := range devs {
		if index < len(ids) {
			parts = append(parts, fmt.Sprintf("%s (%s)", dev, ids[index]))
		} else {
			parts = append(parts, dev)
		}
	}
	return strings.Join(parts, ", ")
}

func wipefs(ctx *Context, devices ...string) error {
	args := append([]string{"wipefs", "--quiet", "--all", "--force"}, devices...)
	if err := ctx.Runner.Try(args[0], args[1:]...); err != nil {
		return fmt.Errorf("could not erase previous file system signatures: %w", err)
	}
	return nil
}

func partprobe(ctx *Context, device string) {
	if ctx.Runner.HasProgram("partprobe") {
		_ = ctx.Runner.Run("partprobe", device)
	}
}

// waitPartition waits for the newly created partition node to appear.
// The device is re-resolved on every attempt: partprobe events can lag
// slightly, and blkid reports "not found yet" (exit status 2) meanwhile.
func waitPartition(ctx *Context, newID string) error {
	// When a command executor is installed (capture/testing mode) no device
	// node can exist, so there is nothing to wait for; skip the retry loop
	// and its sleeps so capture tests are fast and deterministic.
	if ctx.Runner.Exec != nil {
		return nil
	}
	for attempt := 1; attempt <= 10; attempt++ {
		dev, err := resolveID(ctx, newID)
		if err == nil {
			if _, statErr := os.Stat(dev); statErr == nil {
				fmt.Fprintln(ctx.Runner.stderr())
				return nil
			}
		}
		if attempt == 1 {
			fmt.Fprintf(ctx.Runner.stderr(), "Waiting for partition (%s) to appear...", newID)
		}
		fmt.Fprintf(ctx.Runner.stderr(), " %d", 11-attempt)
		time.Sleep(time.Second)
	}
	fmt.Fprintln(ctx.Runner.stderr())
	return fmt.Errorf("partition (%s) did not appear within 10s "+
		"(run partprobe and check dmesg for kernel partition events)", newID)
}

// targetBusyResources returns the mountpoints and active swap devices under
// a whole-disk device that would keep the kernel from switching to a freshly
// written partition table: any mounted filesystem or enabled swap holds the
// old partitions open, so partprobe reports "in use" and the new partition
// nodes never appear (the failure mode when installing onto an auto-mounted
// USB stick).
func (ctx *Context) targetBusyResources(device string) (mounts, swaps []string) {
	if ctx.TargetResources != nil {
		return ctx.TargetResources(device)
	}
	// In capture/testing mode there is no live system to probe; without an
	// injected stub nothing is busy (mirrors waitPartition's Exec fast-path).
	if ctx.Runner.Exec != nil {
		return nil, nil
	}
	out, err := ctx.Runner.QuietRun("lsblk", "--noheadings", "--raw", "--paths",
		"--output", "NAME,MOUNTPOINTS", device)
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 1 {
				mounts = append(mounts, fields[1:]...)
			}
		}
	}
	data, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return mounts, nil
	}
	base, err := filepath.EvalSymlinks(device)
	if err != nil {
		base = device
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[1] != "partition" {
			continue
		}
		if isChildDevice(base, fields[0]) {
			swaps = append(swaps, fields[0])
		}
	}
	return mounts, swaps
}

// isChildDevice reports whether candidate names a partition of the
// whole-disk device base (/dev/sdb -> /dev/sdb1, /dev/nvme0n1 -> /dev/nvme0n1p1).
func isChildDevice(base, candidate string) bool {
	rest := strings.TrimPrefix(candidate, base)
	if rest == "" || rest == candidate {
		return false
	}
	if rest[0] == 'p' {
		rest = rest[1:]
	}
	return len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9'
}

// unmountTarget releases every filesystem and swap under a whole-disk device
// before it is repartitioned. Called from actCreateGPT so the kernel can
// accept the freshly written partition table on the first partprobe.
func unmountTarget(ctx *Context, device string) error {
	mounts, swaps := ctx.targetBusyResources(device)
	if len(mounts) == 0 && len(swaps) == 0 {
		return nil
	}
	ctx.Runner.logf("Releasing filesystems and swap on %s", device)
	for _, mountpoint := range mounts {
		if err := ctx.Runner.Try("umount", mountpoint); err != nil {
			return fmt.Errorf("could not unmount %q (target %s): %w", mountpoint, device, err)
		}
	}
	for _, swap := range swaps {
		if err := ctx.Runner.Try("swapoff", swap); err != nil {
			return fmt.Errorf("could not disable swap on %q (target %s): %w", swap, device, err)
		}
	}
	return nil
}

// ApplyDiskActions executes the layout's action list in order
// (port of apply_disk_actions and all disk_* functions).
func ApplyDiskActions(ctx *Context) error {
	for _, action := range ctx.Layout.Actions {
		var err error
		switch action.Action {
		case disklayout.ActExisting, disklayout.ActCreateDummy:
			// no-op
		case disklayout.ActCreateGPT:
			err = actCreateGPT(ctx, &action)
		case disklayout.ActCreatePartition:
			err = actCreatePartition(ctx, &action)
		case disklayout.ActCreateRaid:
			err = actCreateRaid(ctx, &action)
		case disklayout.ActCreateLuks:
			err = actCreateLuks(ctx, &action)
		case disklayout.ActFormat:
			err = actFormat(ctx, &action)
		case disklayout.ActFormatZFS:
			err = actFormatZFS(ctx, &action)
		case disklayout.ActFormatBtrfs:
			err = actFormatBtrfs(ctx, &action)
		default:
			ctx.Runner.logf("Ignoring invalid action: %s", action.Action)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func operandDevice(ctx *Context, action *disklayout.Action) (string, string, error) {
	// Returns (device, description, error)
	if action.ID != "" {
		dev, err := resolveID(ctx, action.ID)
		if err != nil {
			return "", "", err
		}
		return dev, fmt.Sprintf("%s (%s)", dev, action.ID), nil
	}
	return action.Device, action.Device, nil
}

func actCreateGPT(ctx *Context, action *disklayout.Action) error {
	device, desc, err := operandDevice(ctx, action)
	if err != nil {
		return err
	}
	if err := unmountTarget(ctx, device); err != nil {
		return err
	}
	ptuuid, _ := ctx.Layout.UUIDOf(action.NewID)
	ctx.Runner.logf("Creating new gpt partition table (%s) on %s", action.NewID, desc)
	if err := wipefs(ctx, device); err != nil {
		return err
	}
	if err := ctx.Runner.Try("sgdisk", "-Z", "-U", ptuuid, device); err != nil {
		return fmt.Errorf("could not create new gpt partition table (%s) on '%s': %w",
			action.NewID, device, err)
	}
	partprobe(ctx, device)
	return nil
}

func actCreatePartition(ctx *Context, action *disklayout.Action) error {
	device, err := resolveID(ctx, action.ID)
	if err != nil {
		return err
	}
	argSize := "+" + action.Size
	if action.Size == "remaining" {
		argSize = "0"
	}
	code := disklayout.PartitionTypeCodes[action.Type]
	partuuid, _ := ctx.Layout.UUIDOf(action.NewID)

	args := []string{
		"-n", "0:0:" + argSize,
		"-t", "0:" + code,
		"-u", "0:" + partuuid,
	}
	if action.Type == "bios" {
		args = append(args, "--attributes=0:set:2")
	}
	args = append(args, device)

	ctx.Runner.logf("Creating partition (%s) with type=%s, size=%s on %s",
		action.NewID, action.Type, action.Size, device)
	if err := ctx.Runner.Try("sgdisk", args...); err != nil {
		return fmt.Errorf("could not create new gpt partition (%s) on '%s' (%s): %w",
			action.NewID, device, action.ID, err)
	}
	partprobe(ctx, device)
	return waitPartition(ctx, action.NewID)
}

func actCreateRaid(ctx *Context, action *disklayout.Action) error {
	devs := make([]string, 0, len(action.IDs))
	for _, id := range action.IDs {
		dev, err := resolveID(ctx, id)
		if err != nil {
			return err
		}
		devs = append(devs, dev)
	}
	desc := describeDevices(devs, action.IDs)
	mddevice := "/dev/md/" + action.Name
	uuid, _ := ctx.Layout.UUIDOf(action.NewID)

	metadata := "1.2"
	if action.Level == 1 && action.Name == "efi" {
		metadata = "1.0"
	}

	ctx.Runner.logf("Creating raid%d (%s) on %s", action.Level, action.NewID, desc)
	args := []string{
		"--create", mddevice,
		"--verbose",
		fmt.Sprintf("--level=%d", action.Level),
		fmt.Sprintf("--raid-devices=%d", len(devs)),
		"--uuid=" + uuid,
		"--homehost=" + ctx.Cfg.System.Hostname,
	}
	if metadata == "1.0" {
		args = append(args, "--metadata=1.0")
	} else {
		args = append(args, "--metadata=1.2")
	}
	args = append(args, devs...)
	if err := ctx.Runner.Try("mdadm", args...); err != nil {
		return fmt.Errorf("could not create raid%d array '%s' (%s) on %s: %w",
			action.Level, mddevice, action.NewID, desc, err)
	}
	return nil
}

func actCreateLuks(ctx *Context, action *disklayout.Action) error {
	device, desc, err := operandDevice(ctx, action)
	if err != nil {
		return err
	}
	uuid, _ := ctx.Layout.UUIDOf(action.NewID)

	ctx.Runner.logf("Creating luks (%s) on %s", action.NewID, desc)
	formatArgs := []string{
		"luksFormat",
		"--type", "luks2",
		"--uuid", uuid,
		"--key-file", "-",
		"--cipher", "aes-xts-plain64",
		"--hash", "sha512",
		"--pbkdf", "argon2id",
		"--iter-time", "4000",
		"--key-size", "512",
		"--batch-mode",
		device,
	}
	if err := runWithKey(ctx, formatArgs); err != nil {
		return fmt.Errorf("could not create luks on %s: %w", desc, err)
	}

	if err := ctx.mkdirAll(LuksHeaderBackupDir, 0o755); err != nil {
		return fmt.Errorf("could not create luks header backup dir '%s': %w",
			LuksHeaderBackupDir, err)
	}
	headerFile := filepath.Join(LuksHeaderBackupDir,
		fmt.Sprintf("luks-header-%s-%s.img", action.NewID, strings.ToLower(uuid)))
	_ = ctx.removeAll(headerFile)
	if err := ctx.Runner.Try("cryptsetup", "luksHeaderBackup", device,
		"--header-backup-file", headerFile); err != nil {
		return fmt.Errorf("could not backup luks header on %s: %w", desc, err)
	}

	if err := runWithKey(ctx, []string{"open", "--type", "luks2",
		"--key-file", "-", device, action.Name}); err != nil {
		return fmt.Errorf("could not open luks encrypted device %s: %w", desc, err)
	}
	return nil
}

// runWithKey runs cryptsetup feeding the encryption key on stdin.
func runWithKey(ctx *Context, args []string) error {
	return ctx.Runner.RunWithStdin(ctx.EncryptionKey, "cryptsetup", args...)
}

func initBtrfs(ctx *Context, device, desc string) error {
	if err := ctx.mkdirAll("/btrfs", 0o755); err != nil {
		return fmt.Errorf("could not create /btrfs directory: %w", err)
	}
	if err := ctx.Runner.Try("mount", device, "/btrfs"); err != nil {
		return fmt.Errorf("could not mount %s to /btrfs: %w", desc, err)
	}
	if err := ctx.Runner.Try("btrfs", "subvolume", "create", "/btrfs/root"); err != nil {
		return fmt.Errorf("could not create btrfs subvolume /root on %s: %w", desc, err)
	}
	if err := ctx.Runner.Try("btrfs", "subvolume", "set-default", "/btrfs/root"); err != nil {
		return fmt.Errorf("could not set default btrfs subvolume to /root on %s: %w", desc, err)
	}
	if err := ctx.Runner.Try("umount", "/btrfs"); err != nil {
		return fmt.Errorf("could not unmount btrfs on %s: %w", desc, err)
	}
	return nil
}

func actFormat(ctx *Context, action *disklayout.Action) error {
	device, err := resolveID(ctx, action.ID)
	if err != nil {
		return err
	}
	ctx.Runner.logf("Formatting %s (%s) with %s", device, action.ID, action.Type)
	if err := wipefs(ctx, device); err != nil {
		return fmt.Errorf("could not erase previous file system signatures from '%s' (%s): %w",
			device, action.ID, err)
	}

	switch action.Type {
	case "bios", "efi":
		args := []string{"mkfs.fat", "-F", "32"}
		if action.Label != "" {
			args = append(args, "-n", action.Label)
		}
		args = append(args, device)
		if err := ctx.Runner.Try(args[0], args[1:]...); err != nil {
			return fmt.Errorf("could not format device '%s' (%s): %w", device, action.ID, err)
		}
	case "swap":
		args := []string{"mkswap"}
		if action.Label != "" {
			args = append(args, "-L", action.Label)
		}
		args = append(args, device)
		if err := ctx.Runner.Try(args[0], args[1:]...); err != nil {
			return fmt.Errorf("could not format device '%s' (%s): %w", device, action.ID, err)
		}
		// Try to disable an automatically enabled swap.
		_ = ctx.Runner.Run("swapoff", device)
	case "ext4":
		args := []string{"mkfs.ext4", "-q"}
		if action.Label != "" {
			args = append(args, "-L", action.Label)
		}
		args = append(args, device)
		if err := ctx.Runner.Try(args[0], args[1:]...); err != nil {
			return fmt.Errorf("could not format device '%s' (%s): %w", device, action.ID, err)
		}
	case "btrfs":
		args := []string{"mkfs.btrfs", "-q"}
		if action.Label != "" {
			args = append(args, "-L", action.Label)
		}
		args = append(args, device)
		if err := ctx.Runner.Try(args[0], args[1:]...); err != nil {
			return fmt.Errorf("could not format device '%s' (%s): %w", device, action.ID, err)
		}
		if err := initBtrfs(ctx, device, fmt.Sprintf("'%s' (%s)", device, action.ID)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown filesystem type %q", action.Type)
	}
	return nil
}

func actFormatZFS(ctx *Context, action *disklayout.Action) error {
	devs := make([]string, 0, len(action.IDs))
	for _, id := range action.IDs {
		dev, err := resolveID(ctx, id)
		if err != nil {
			return err
		}
		devs = append(devs, dev)
	}
	desc := describeDevices(devs, action.IDs)

	if err := wipefs(ctx, devs...); err != nil {
		return fmt.Errorf("could not erase previous file system signatures from %s: %w", desc, err)
	}

	if action.PoolType == "custom" {
		return fmt.Errorf(
			"custom zfs pool type requires manual pool creation; " +
				"use pool_type=\"standard\" or partition via [disk.custom]")
	}

	ctx.Runner.logf("Creating zfs pool on %s", desc)
	args := []string{
		"create",
		"-R", RootMountpoint,
		"-o", "ashift=12",
		"-O", "acltype=posix",
		"-O", "atime=off",
		"-O", "xattr=sa",
		"-O", "dnodesize=auto",
		"-O", "mountpoint=none",
		"-O", "canmount=noauto",
		"-O", "devices=off",
	}
	stdin := ""
	if action.Encrypt {
		args = append(args,
			"-O", "encryption=aes-256-gcm",
			"-O", "keyformat=passphrase",
			"-O", "keylocation=prompt")
		stdin = ctx.EncryptionKey + "\n"
	}
	args = append(args, "rpool")
	args = append(args, devs...)

	if err := ctx.Runner.RunWithStdin(stdin, "zpool", args...); err != nil {
		return fmt.Errorf("could not create zfs pool on %s: %w", desc, err)
	}

	if action.Compress != "" {
		if err := ctx.Runner.Try("zfs", "set", "compression="+action.Compress, "rpool"); err != nil {
			return fmt.Errorf("could not enable compression on dataset 'rpool': %w", err)
		}
	}
	if err := ctx.Runner.Try("zfs", "create", "rpool/ROOT"); err != nil {
		return fmt.Errorf("could not create zfs dataset 'rpool/ROOT': %w", err)
	}
	if err := ctx.Runner.Try("zfs", "create", "-o", "mountpoint=/", "rpool/ROOT/default"); err != nil {
		return fmt.Errorf("could not create zfs dataset 'rpool/ROOT/default': %w", err)
	}
	if err := ctx.Runner.Try("zpool", "set", "bootfs=rpool/ROOT/default", "rpool"); err != nil {
		return fmt.Errorf("could not set zfs property bootfs on rpool: %w", err)
	}
	return nil
}

func actFormatBtrfs(ctx *Context, action *disklayout.Action) error {
	devs := make([]string, 0, len(action.IDs))
	for _, id := range action.IDs {
		dev, err := resolveID(ctx, id)
		if err != nil {
			return err
		}
		devs = append(devs, dev)
	}
	desc := describeDevices(devs, action.IDs)

	if err := wipefs(ctx, devs...); err != nil {
		return fmt.Errorf("could not erase previous file system signatures from %s: %w", desc, err)
	}

	args := []string{"mkfs.btrfs", "-q"}
	if len(devs) > 1 && action.RaidType != "" {
		args = append(args, "-d", action.RaidType)
	}
	if action.Label != "" {
		args = append(args, "-L", action.Label)
	}
	args = append(args, devs...)

	ctx.Runner.logf("Creating btrfs on %s", desc)
	if err := ctx.Runner.Try(args[0], args[1:]...); err != nil {
		return fmt.Errorf("could not create btrfs on %s: %w", desc, err)
	}
	return initBtrfs(ctx, devs[0], fmt.Sprintf("btrfs array (%s)", desc))
}
