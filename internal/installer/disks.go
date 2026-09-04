package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gentooinstall/internal/disklayout"
)

// resolveID resolves a layout id, returning a canonicalized device path.
func resolveID(c *Context, id string) (string, error) {
	dev, err := c.Resolver.ResolveDevice(id)
	if err != nil {
		return "", fmt.Errorf("could not resolve device with id=%s: %w", id, err)
	}
	return dev, nil
}

// describeDevices renders "dev (id), dev2 (id2)" for log messages.
func describeDevices(devs []string, ids []string) string {
	var parts []string
	for i, d := range devs {
		if i < len(ids) {
			parts = append(parts, fmt.Sprintf("%s (%s)", d, ids[i]))
		} else {
			parts = append(parts, d)
		}
	}
	return strings.Join(parts, ", ")
}

func wipefs(c *Context, devices ...string) error {
	args := append([]string{"wipefs", "--quiet", "--all", "--force"}, devices...)
	if err := c.R.Try(args[0], args[1:]...); err != nil {
		return fmt.Errorf("could not erase previous file system signatures: %w", err)
	}
	return nil
}

func partprobe(c *Context, device string) {
	if c.R.HasProgram("partprobe") {
		_ = c.R.Run("partprobe", device)
	}
}

// waitPartition waits for the newly created partition node to appear.
// The device is re-resolved on every attempt: partprobe events can lag
// slightly, and blkid reports "not found yet" (exit status 2) meanwhile.
func waitPartition(c *Context, newID string) error {
	// When a command executor is installed (capture/testing mode) no device
	// node can exist, so there is nothing to wait for; skip the retry loop
	// and its sleeps so capture tests are fast and deterministic.
	if c.R.Exec != nil {
		return nil
	}
	for i := 1; i <= 10; i++ {
		dev, err := resolveID(c, newID)
		if err == nil {
			if _, statErr := os.Stat(dev); statErr == nil {
				fmt.Fprintln(c.R.stderr())
				return nil
			}
		}
		if i == 1 {
			fmt.Fprintf(c.R.stderr(), "Waiting for partition (%s) to appear...", newID)
		}
		fmt.Fprintf(c.R.stderr(), " %d", 11-i)
		time.Sleep(time.Second)
	}
	fmt.Fprintln(c.R.stderr())
	return nil // proceed optimistically, matching bash behavior
}

// ApplyDiskActions executes the layout's action list in order
// (port of apply_disk_actions and all disk_* functions).
func ApplyDiskActions(c *Context) error {
	for _, a := range c.Layout.Actions {
		var err error
		switch a.Action {
		case disklayout.ActExisting, disklayout.ActCreateDummy:
			// no-op
		case disklayout.ActCreateGPT:
			err = actCreateGPT(c, &a)
		case disklayout.ActCreatePartition:
			err = actCreatePartition(c, &a)
		case disklayout.ActCreateRaid:
			err = actCreateRaid(c, &a)
		case disklayout.ActCreateLuks:
			err = actCreateLuks(c, &a)
		case disklayout.ActFormat:
			err = actFormat(c, &a)
		case disklayout.ActFormatZFS:
			err = actFormatZFS(c, &a)
		case disklayout.ActFormatBtrfs:
			err = actFormatBtrfs(c, &a)
		default:
			c.R.logf("Ignoring invalid action: %s", a.Action)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func operandDevice(c *Context, a *disklayout.Action) (string, string, error) {
	// Returns (device, description, error)
	if a.ID != "" {
		dev, err := resolveID(c, a.ID)
		if err != nil {
			return "", "", err
		}
		return dev, fmt.Sprintf("%s (%s)", dev, a.ID), nil
	}
	return a.Device, a.Device, nil
}

func actCreateGPT(c *Context, a *disklayout.Action) error {
	device, desc, err := operandDevice(c, a)
	if err != nil {
		return err
	}
	ptuuid, _ := c.Layout.UUIDOf(a.NewID)
	c.R.logf("Creating new gpt partition table (%s) on %s", a.NewID, desc)
	if err := wipefs(c, device); err != nil {
		return err
	}
	if err := c.R.Try("sgdisk", "-Z", "-U", ptuuid, device); err != nil {
		return fmt.Errorf("could not create new gpt partition table (%s) on '%s': %w",
			a.NewID, device, err)
	}
	partprobe(c, device)
	return nil
}

func actCreatePartition(c *Context, a *disklayout.Action) error {
	device, err := resolveID(c, a.ID)
	if err != nil {
		return err
	}
	argSize := "+" + a.Size
	if a.Size == "remaining" {
		argSize = "0"
	}
	code := disklayout.PartitionTypeCodes[a.Type]
	partuuid, _ := c.Layout.UUIDOf(a.NewID)

	args := []string{
		"-n", "0:0:" + argSize,
		"-t", "0:" + code,
		"-u", "0:" + partuuid,
	}
	if a.Type == "bios" {
		args = append(args, "--attributes=0:set:2")
	}
	args = append(args, device)

	c.R.logf("Creating partition (%s) with type=%s, size=%s on %s",
		a.NewID, a.Type, a.Size, device)
	if err := c.R.Try("sgdisk", args...); err != nil {
		return fmt.Errorf("could not create new gpt partition (%s) on '%s' (%s): %w",
			a.NewID, device, a.ID, err)
	}
	partprobe(c, device)
	return waitPartition(c, a.NewID)
}

func actCreateRaid(c *Context, a *disklayout.Action) error {
	devs := make([]string, 0, len(a.IDs))
	for _, id := range a.IDs {
		dev, err := resolveID(c, id)
		if err != nil {
			return err
		}
		devs = append(devs, dev)
	}
	desc := describeDevices(devs, a.IDs)
	mddevice := "/dev/md/" + a.Name
	uuid, _ := c.Layout.UUIDOf(a.NewID)

	metadata := "1.2"
	if a.Level == 1 && a.Name == "efi" {
		metadata = "1.0"
	}

	c.R.logf("Creating raid%d (%s) on %s", a.Level, a.NewID, desc)
	args := []string{
		"--create", mddevice,
		"--verbose",
		fmt.Sprintf("--level=%d", a.Level),
		fmt.Sprintf("--raid-devices=%d", len(devs)),
		"--uuid=" + uuid,
		"--homehost=" + c.Cfg.System.Hostname,
	}
	if metadata == "1.0" {
		args = append(args, "--metadata=1.0")
	} else {
		args = append(args, "--metadata=1.2")
	}
	args = append(args, devs...)
	if err := c.R.Try("mdadm", args...); err != nil {
		return fmt.Errorf("could not create raid%d array '%s' (%s) on %s: %w",
			a.Level, mddevice, a.NewID, desc, err)
	}
	return nil
}

func actCreateLuks(c *Context, a *disklayout.Action) error {
	device, desc, err := operandDevice(c, a)
	if err != nil {
		return err
	}
	uuid, _ := c.Layout.UUIDOf(a.NewID)

	c.R.logf("Creating luks (%s) on %s", a.NewID, desc)
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
	if err := runWithKey(c, formatArgs); err != nil {
		return fmt.Errorf("could not create luks on %s: %w", desc, err)
	}

	if err := c.mkdirAll(LuksHeaderBackupDir, 0o755); err != nil {
		return fmt.Errorf("could not create luks header backup dir '%s': %w",
			LuksHeaderBackupDir, err)
	}
	headerFile := filepath.Join(LuksHeaderBackupDir,
		fmt.Sprintf("luks-header-%s-%s.img", a.NewID, strings.ToLower(uuid)))
	_ = c.removeAll(headerFile)
	if err := c.R.Try("cryptsetup", "luksHeaderBackup", device,
		"--header-backup-file", headerFile); err != nil {
		return fmt.Errorf("could not backup luks header on %s: %w", desc, err)
	}

	if err := runWithKey(c, []string{"open", "--type", "luks2",
		"--key-file", "-", device, a.Name}); err != nil {
		return fmt.Errorf("could not open luks encrypted device %s: %w", desc, err)
	}
	return nil
}

// runWithKey runs cryptsetup feeding the encryption key on stdin.
func runWithKey(c *Context, args []string) error {
	return c.R.RunWithStdin(c.EncryptionKey, "cryptsetup", args...)
}

func initBtrfs(c *Context, device, desc string) error {
	if err := c.mkdirAll("/btrfs", 0o755); err != nil {
		return fmt.Errorf("could not create /btrfs directory: %w", err)
	}
	if err := c.R.Try("mount", device, "/btrfs"); err != nil {
		return fmt.Errorf("could not mount %s to /btrfs: %w", desc, err)
	}
	if err := c.R.Try("btrfs", "subvolume", "create", "/btrfs/root"); err != nil {
		return fmt.Errorf("could not create btrfs subvolume /root on %s: %w", desc, err)
	}
	if err := c.R.Try("btrfs", "subvolume", "set-default", "/btrfs/root"); err != nil {
		return fmt.Errorf("could not set default btrfs subvolume to /root on %s: %w", desc, err)
	}
	if err := c.R.Try("umount", "/btrfs"); err != nil {
		return fmt.Errorf("could not unmount btrfs on %s: %w", desc, err)
	}
	return nil
}

func actFormat(c *Context, a *disklayout.Action) error {
	device, err := resolveID(c, a.ID)
	if err != nil {
		return err
	}
	c.R.logf("Formatting %s (%s) with %s", device, a.ID, a.Type)
	if err := wipefs(c, device); err != nil {
		return fmt.Errorf("could not erase previous file system signatures from '%s' (%s): %w",
			device, a.ID, err)
	}

	switch a.Type {
	case "bios", "efi":
		args := []string{"mkfs.fat", "-F", "32"}
		if a.Label != "" {
			args = append(args, "-n", a.Label)
		}
		args = append(args, device)
		if err := c.R.Try(args[0], args[1:]...); err != nil {
			return fmt.Errorf("could not format device '%s' (%s): %w", device, a.ID, err)
		}
	case "swap":
		args := []string{"mkswap"}
		if a.Label != "" {
			args = append(args, "-L", a.Label)
		}
		args = append(args, device)
		if err := c.R.Try(args[0], args[1:]...); err != nil {
			return fmt.Errorf("could not format device '%s' (%s): %w", device, a.ID, err)
		}
		// Try to disable an automatically enabled swap.
		_ = c.R.Run("swapoff", device)
	case "ext4":
		args := []string{"mkfs.ext4", "-q"}
		if a.Label != "" {
			args = append(args, "-L", a.Label)
		}
		args = append(args, device)
		if err := c.R.Try(args[0], args[1:]...); err != nil {
			return fmt.Errorf("could not format device '%s' (%s): %w", device, a.ID, err)
		}
	case "btrfs":
		args := []string{"mkfs.btrfs", "-q"}
		if a.Label != "" {
			args = append(args, "-L", a.Label)
		}
		args = append(args, device)
		if err := c.R.Try(args[0], args[1:]...); err != nil {
			return fmt.Errorf("could not format device '%s' (%s): %w", device, a.ID, err)
		}
		if err := initBtrfs(c, device, fmt.Sprintf("'%s' (%s)", device, a.ID)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown filesystem type %q", a.Type)
	}
	return nil
}

func actFormatZFS(c *Context, a *disklayout.Action) error {
	devs := make([]string, 0, len(a.IDs))
	for _, id := range a.IDs {
		dev, err := resolveID(c, id)
		if err != nil {
			return err
		}
		devs = append(devs, dev)
	}
	desc := describeDevices(devs, a.IDs)

	if err := wipefs(c, devs...); err != nil {
		return fmt.Errorf("could not erase previous file system signatures from %s: %w", desc, err)
	}

	if a.PoolType == "custom" {
		return fmt.Errorf(
			"custom zfs pool type requires manual pool creation; " +
				"use pool_type=\"standard\" or partition via [disk.custom]")
	}

	c.R.logf("Creating zfs pool on %s", desc)
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
	if a.Encrypt {
		args = append(args,
			"-O", "encryption=aes-256-gcm",
			"-O", "keyformat=passphrase",
			"-O", "keylocation=prompt")
		stdin = c.EncryptionKey + "\n"
	}
	args = append(args, "rpool")
	args = append(args, devs...)

	if err := c.R.RunWithStdin(stdin, "zpool", args...); err != nil {
		return fmt.Errorf("could not create zfs pool on %s: %w", desc, err)
	}

	if a.Compress != "" {
		if err := c.R.Try("zfs", "set", "compression="+a.Compress, "rpool"); err != nil {
			return fmt.Errorf("could not enable compression on dataset 'rpool': %w", err)
		}
	}
	if err := c.R.Try("zfs", "create", "rpool/ROOT"); err != nil {
		return fmt.Errorf("could not create zfs dataset 'rpool/ROOT': %w", err)
	}
	if err := c.R.Try("zfs", "create", "-o", "mountpoint=/", "rpool/ROOT/default"); err != nil {
		return fmt.Errorf("could not create zfs dataset 'rpool/ROOT/default': %w", err)
	}
	if err := c.R.Try("zpool", "set", "bootfs=rpool/ROOT/default", "rpool"); err != nil {
		return fmt.Errorf("could not set zfs property bootfs on rpool: %w", err)
	}
	return nil
}

func actFormatBtrfs(c *Context, a *disklayout.Action) error {
	devs := make([]string, 0, len(a.IDs))
	for _, id := range a.IDs {
		dev, err := resolveID(c, id)
		if err != nil {
			return err
		}
		devs = append(devs, dev)
	}
	desc := describeDevices(devs, a.IDs)

	if err := wipefs(c, devs...); err != nil {
		return fmt.Errorf("could not erase previous file system signatures from %s: %w", desc, err)
	}

	args := []string{"mkfs.btrfs", "-q"}
	if len(devs) > 1 && a.RaidType != "" {
		args = append(args, "-d", a.RaidType)
	}
	if a.Label != "" {
		args = append(args, "-L", a.Label)
	}
	args = append(args, devs...)

	c.R.logf("Creating btrfs on %s", desc)
	if err := c.R.Try(args[0], args[1:]...); err != nil {
		return fmt.Errorf("could not create btrfs on %s: %w", desc, err)
	}
	return initBtrfs(c, devs[0], fmt.Sprintf("btrfs array (%s)", desc))
}
