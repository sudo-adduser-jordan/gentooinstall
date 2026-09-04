package tests

import (
	"strings"
	"testing"
	"time"

	"gentooinstall/internal/config"
	"gentooinstall/internal/disklayout"
	"gentooinstall/internal/installer"
)

const (
	uGpt      = "00000000-0000-0000-0000-000000000001"
	uEfi      = "00000000-0000-0000-0000-000000000002"
	uSwap     = "00000000-0000-0000-0000-000000000003"
	uRoot     = "00000000-0000-0000-0000-000000000004"
	uLuksRoot = "00000000-0000-0000-0000-000000000005"
	uGPT0     = "00000000-0000-0000-0000-000000000010"
	uEfi0     = "00000000-0000-0000-0000-000000000011"
	uRoot0    = "00000000-0000-0000-0000-000000000012"
	uRaidEfi  = "00000000-0000-0000-0000-000000000013"
	uRaidSwap = "00000000-0000-0000-0000-000000000014"
	uRaidRoot = "00000000-0000-0000-0000-000000000015"
	uSwap0    = "00000000-0000-0000-0000-000000000016"
	uSwap1    = "00000000-0000-0000-0000-000000000017"
	uEfi1     = "00000000-0000-0000-0000-000000000018"
	uRoot1    = "00000000-0000-0000-0000-000000000019"
)

func classicSeeds() map[string]string {
	return map[string]string{
		"gpt": uGpt, "part_efi": uEfi, "part_swap": uSwap,
		"part_root": uRoot, "part_luks_root": uLuksRoot,
	}
}

func TestApplyDiskActionsClassicEFILuksSwap(t *testing.T) {
	cfg := classicCfg("/dev/sdX", true, false)
	c, s := testContext(t, cfg, classicSeeds())

	start := time.Now()
	if err := installer.ApplyDiskActions(c); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		// waitPartition must be skipped when capturing, otherwise the 10x1s
		// retry loop would make this test crawl.
		t.Fatalf("ApplyDiskActions took %v; waitPartition did not fast-path", elapsed)
	}

	assertCmds(t, s,
		"wipefs --quiet --all --force /dev/sdX",
		"sgdisk -Z -U "+uGpt+" /dev/sdX",
		"sgdisk -n 0:0:+1GiB -t 0:ef00 -u 0:"+uEfi+" /dev/fake-gpt",
		"sgdisk -n 0:0:+8GiB -t 0:8200 -u 0:"+uSwap+" /dev/fake-gpt",
		"sgdisk -n 0:0:0 -t 0:8300 -u 0:"+uRoot+" /dev/fake-gpt",
		"cryptsetup luksFormat --type luks2 --uuid "+uLuksRoot+" --key-file - --cipher aes-xts-plain64 --hash sha512 --pbkdf argon2id --iter-time 4000 --key-size 512 --batch-mode /dev/fake-part_root",
		"cryptsetup luksHeaderBackup /dev/fake-part_root --header-backup-file /tmp/gentoo-install/luks-headers/luks-header-part_luks_root-"+uLuksRoot+".img",
		"cryptsetup open --type luks2 --key-file - /dev/fake-part_root root",
		"wipefs --quiet --all --force /dev/fake-part_efi",
		"mkfs.fat -F 32 -n efi /dev/fake-part_efi",
		"wipefs --quiet --all --force /dev/fake-part_swap",
		"mkswap -L swap /dev/fake-part_swap",
		"swapoff /dev/fake-part_swap",
		"wipefs --quiet --all --force /dev/fake-part_luks_root",
		"mkfs.ext4 -q -L root /dev/fake-part_luks_root",
	)

	// cryptsetup keyed operations must feed the passphrase on stdin.
	calls := s.Calls()
	if calls[5].Stdin != "test-passphrase" {
		t.Fatalf("luksFormat stdin = %q", calls[5].Stdin)
	}
	if calls[7].Stdin != "test-passphrase" {
		t.Fatalf("luks open stdin = %q", calls[7].Stdin)
	}
}

func TestApplyDiskActionsClassicBiosNoLuksNoSwapBtrfs(t *testing.T) {
	cfg := classicCfg("/dev/sdY", false, true)
	cfg.Disk.BootType = "bios"
	cfg.Disk.UseSwap = false
	c, s := testContext(t, cfg, map[string]string{"gpt": uGpt, "part_bios": uEfi, "part_root": uRoot})

	if err := installer.ApplyDiskActions(c); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, s,
		"wipefs --quiet --all --force /dev/sdY",
		"sgdisk -Z -U "+uGpt+" /dev/sdY",
		"sgdisk -n 0:0:+1GiB -t 0:ef02 -u 0:"+uEfi+" --attributes=0:set:2 /dev/fake-gpt",
		"sgdisk -n 0:0:0 -t 0:8300 -u 0:"+uRoot+" /dev/fake-gpt",
		"wipefs --quiet --all --force /dev/fake-part_bios",
		"mkfs.fat -F 32 -n bios /dev/fake-part_bios",
		"wipefs --quiet --all --force /dev/fake-part_root",
		"mkfs.btrfs -q -L root /dev/fake-part_root",
		"mount /dev/fake-part_root /btrfs",
		"btrfs subvolume create /btrfs/root",
		"btrfs subvolume set-default /btrfs/root",
		"umount /btrfs",
	)
}

func TestApplyDiskActionsBtrfsCentricRaid(t *testing.T) {
	cfg := config.Default(true)
	cfg.Disk.Scheme = config.SchemeBtrfs
	cfg.Disk.Devices = []string{"/dev/sda", "/dev/sdb"}
	cfg.Disk.UseSwap = false
	cfg.Disk.UseLuks = false
	c, s := testContext(t, cfg, map[string]string{
		"gpt_dev0": uGPT0, "part_efi_dev0": uEfi0, "part_root_dev0": uRoot0, "root_dev1": uRoot1,
	})

	if err := installer.ApplyDiskActions(c); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, s,
		"wipefs --quiet --all --force /dev/sda",
		"sgdisk -Z -U "+uGPT0+" /dev/sda",
		"sgdisk -n 0:0:+1GiB -t 0:ef00 -u 0:"+uEfi0+" /dev/fake-gpt_dev0",
		"sgdisk -n 0:0:0 -t 0:8300 -u 0:"+uRoot0+" /dev/fake-gpt_dev0",
		"wipefs --quiet --all --force /dev/fake-part_efi_dev0",
		"mkfs.fat -F 32 -n efi /dev/fake-part_efi_dev0",
		"wipefs --quiet --all --force /dev/fake-part_root_dev0 /dev/fake-root_dev1",
		"mkfs.btrfs -q -d raid0 -L root /dev/fake-part_root_dev0 /dev/fake-root_dev1",
		"mount /dev/fake-part_root_dev0 /btrfs",
		"btrfs subvolume create /btrfs/root",
		"btrfs subvolume set-default /btrfs/root",
		"umount /btrfs",
	)
}

func TestApplyDiskActionsZFSCentricEncrypted(t *testing.T) {
	cfg := config.Default(true)
	cfg.Disk.Scheme = config.SchemeZFSCentric
	cfg.Disk.Devices = []string{"/dev/sda", "/dev/sdb"}
	cfg.Disk.UseSwap = false
	cfg.Disk.ZFSEncrypt = true
	cfg.Disk.ZFSUseCompress = true
	cfg.Disk.ZFSCompression = "zstd"
	c, s := testContext(t, cfg, map[string]string{
		"gpt_dev0": uGPT0, "part_efi_dev0": uEfi0, "part_root_dev0": uRoot0, "root_dev1": uRoot1,
	})

	if err := installer.ApplyDiskActions(c); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, s,
		"wipefs --quiet --all --force /dev/sda",
		"sgdisk -Z -U "+uGPT0+" /dev/sda",
		"sgdisk -n 0:0:+1GiB -t 0:ef00 -u 0:"+uEfi0+" /dev/fake-gpt_dev0",
		"sgdisk -n 0:0:0 -t 0:8300 -u 0:"+uRoot0+" /dev/fake-gpt_dev0",
		"wipefs --quiet --all --force /dev/fake-part_efi_dev0",
		"mkfs.fat -F 32 -n efi /dev/fake-part_efi_dev0",
		"wipefs --quiet --all --force /dev/fake-part_root_dev0 /dev/fake-root_dev1",
		"zpool create -R /tmp/gentoo-install/root -o ashift=12 -O acltype=posix -O atime=off -O xattr=sa -O dnodesize=auto -O mountpoint=none -O canmount=noauto -O devices=off -O encryption=aes-256-gcm -O keyformat=passphrase -O keylocation=prompt rpool /dev/fake-part_root_dev0 /dev/fake-root_dev1",
		"zfs set compression=zstd rpool",
		"zfs create rpool/ROOT",
		"zfs create -o mountpoint=/ rpool/ROOT/default",
		"zpool set bootfs=rpool/ROOT/default rpool",
	)

	calls := s.Calls()
	if calls[7].Stdin != "test-passphrase\n" {
		t.Fatalf("zpool stdin = %q, want passphrase+newline", calls[7].Stdin)
	}
}

func TestApplyDiskActionsRaid1Luks(t *testing.T) {
	cfg := config.Default(true)
	cfg.Disk.Scheme = config.SchemeRaid1Luks
	cfg.Disk.Devices = []string{"/dev/sda", "/dev/sdb"}
	cfg.Disk.BootType = "bios"
	cfg.Disk.UseLuks = true
	c, s := testContext(t, cfg, map[string]string{
		"gpt_dev0": uGPT0, "part_bios_dev0": uEfi0, "part_swap_dev0": uSwap0, "part_root_dev0": uRoot0,
		"gpt_dev1": uGPT0, "part_bios_dev1": uEfi1, "part_swap_dev1": uSwap1, "part_root_dev1": uRoot1,
		"part_raid_bios": uRaidEfi, "part_raid_swap": uRaidSwap, "part_raid_root": uRaidRoot,
		"part_luks_root": uLuksRoot,
	})

	if err := installer.ApplyDiskActions(c); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"wipefs --quiet --all --force /dev/sda",
		"sgdisk -Z -U " + uGPT0 + " /dev/sda",
		"sgdisk -n 0:0:+1GiB -t 0:ef02 -u 0:" + uEfi0 + " --attributes=0:set:2 /dev/fake-gpt_dev0",
		"sgdisk -n 0:0:+8GiB -t 0:fd00 -u 0:" + uSwap0 + " /dev/fake-gpt_dev0",
		"sgdisk -n 0:0:0 -t 0:fd00 -u 0:" + uRoot0 + " /dev/fake-gpt_dev0",
		"wipefs --quiet --all --force /dev/sdb",
		"sgdisk -Z -U " + uGPT0 + " /dev/sdb",
		"sgdisk -n 0:0:+1GiB -t 0:ef02 -u 0:" + uEfi1 + " --attributes=0:set:2 /dev/fake-gpt_dev1",
		"sgdisk -n 0:0:+8GiB -t 0:fd00 -u 0:" + uSwap1 + " /dev/fake-gpt_dev1",
		"sgdisk -n 0:0:0 -t 0:fd00 -u 0:" + uRoot1 + " /dev/fake-gpt_dev1",
		"mdadm --create /dev/md/bios --verbose --level=1 --raid-devices=2 --uuid=" + uRaidEfi + " --homehost=gentoo --metadata=1.2 /dev/fake-part_bios_dev0 /dev/fake-part_bios_dev1",
		"mdadm --create /dev/md/swap --verbose --level=1 --raid-devices=2 --uuid=" + uRaidSwap + " --homehost=gentoo --metadata=1.2 /dev/fake-part_swap_dev0 /dev/fake-part_swap_dev1",
		"mdadm --create /dev/md/root --verbose --level=1 --raid-devices=2 --uuid=" + uRaidRoot + " --homehost=gentoo --metadata=1.2 /dev/fake-part_root_dev0 /dev/fake-part_root_dev1",
		"cryptsetup luksFormat --type luks2 --uuid " + uLuksRoot + " --key-file - --cipher aes-xts-plain64 --hash sha512 --pbkdf argon2id --iter-time 4000 --key-size 512 --batch-mode /dev/fake-part_raid_root",
		"cryptsetup luksHeaderBackup /dev/fake-part_raid_root --header-backup-file /tmp/gentoo-install/luks-headers/luks-header-part_luks_root-" + uLuksRoot + ".img",
		"cryptsetup open --type luks2 --key-file - /dev/fake-part_raid_root root",
		"wipefs --quiet --all --force /dev/fake-part_raid_bios",
		"mkfs.fat -F 32 -n bios /dev/fake-part_raid_bios",
		"wipefs --quiet --all --force /dev/fake-part_raid_swap",
		"mkswap -L swap /dev/fake-part_raid_swap",
		"swapoff /dev/fake-part_raid_swap",
		"wipefs --quiet --all --force /dev/fake-part_luks_root",
		"mkfs.ext4 -q -L root /dev/fake-part_luks_root",
	}
	assertCmds(t, s, want...)
}

func TestApplyDiskActionsPartprobeWhenAvailable(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	c, s := testContext(t, cfg, map[string]string{"gpt": uGpt, "part_efi": uEfi, "part_root": uRoot})
	c.R.LookPath = func(name string) bool { return name == "partprobe" }

	if err := installer.ApplyDiskActions(c); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, s,
		"wipefs --quiet --all --force /dev/sdX",
		"sgdisk -Z -U "+uGpt+" /dev/sdX",
		"partprobe /dev/sdX",
		"sgdisk -n 0:0:+1GiB -t 0:ef00 -u 0:"+uEfi+" /dev/fake-gpt",
		"partprobe /dev/fake-gpt",
		"sgdisk -n 0:0:0 -t 0:8300 -u 0:"+uRoot+" /dev/fake-gpt",
		"partprobe /dev/fake-gpt",
		"wipefs --quiet --all --force /dev/fake-part_efi",
		"mkfs.fat -F 32 -n efi /dev/fake-part_efi",
		"wipefs --quiet --all --force /dev/fake-part_root",
		"mkfs.ext4 -q -L root /dev/fake-part_root",
	)
}

func TestApplyDiskActionsSwallowSwapoffFailure(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, s := testContext(t, cfg, classicSeeds())
	// swapoff runs best-effort; even when it is scripted to fail the apply
	// must succeed (the failure is swallowed with `_ =`).
	s.FailOn = []string{"swapoff"}

	if err := installer.ApplyDiskActions(c); err != nil {
		t.Fatalf("swapoff failure must be ignored: %v", err)
	}
	if !strings.Contains(strings.Join(s.Lines(), "\n"), "swapoff") {
		t.Fatal("swapoff not recorded")
	}
}

func TestApplyDiskActionsPropagatesSgdiskError(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Disk.UseSwap = false
	c, s := testContext(t, cfg, map[string]string{"gpt": uGpt, "part_efi": uEfi, "part_root": uRoot})
	s.FailOn = []string{"sgdisk"}

	err := installer.ApplyDiskActions(c)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sgdisk") {
		t.Fatalf("error should mention sgdisk: %v", err)
	}
}

func TestApplyDiskActionsSkipsUnknownAction(t *testing.T) {
	stub := NewExecStub()
	r := installer.NewRunner(discardWriter{t}, discardWriter{t})
	r.Exec = stub
	r.OnFailure = installer.DefaultOnFailure
	c := &installer.Context{R: r, Layout: &disklayout.Layout{
		Actions: []disklayout.Action{{Action: disklayout.ActionKind("not_a_real_action")}},
	}}

	if err := installer.ApplyDiskActions(c); err != nil {
		t.Fatalf("unknown action must be ignored, got %v", err)
	}
	if len(stub.Calls()) != 0 {
		t.Fatalf("unknown action ran commands: %v", stub.Lines())
	}
}
