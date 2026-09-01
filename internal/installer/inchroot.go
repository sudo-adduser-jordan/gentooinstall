package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"gentooinstall/internal/config"
)

// SetupChrootEnv applies the environment from dispatch_chroot.sh.
func SetupChrootEnv(c *Context) {
	setUmask(0o077)
	nproc := runtime.NumCPU()
	if nproc < 2 {
		nproc = 2 // parity with nproc || echo 2 edge cases
	}
	c.NProc = nproc
	_ = os.Setenv("NPROC", fmt.Sprint(nproc))
	_ = os.Setenv("NPROC_ONE", fmt.Sprint(nproc+1))
	_ = os.Setenv("MAKEFLAGS", "-j"+fmt.Sprint(nproc))
	_ = os.Setenv("EMERGE_DEFAULT_OPTS",
		fmt.Sprintf("--jobs=%d --load-average=%d", nproc+1, nproc))
}

func appendFile(c *Context, path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("could not write to %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return err
	}
	return nil
}

func replaceLine(path, matchPrefix, replacement string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`(?m)^.*` + regexp.QuoteMeta(matchPrefix) + `.*$`)
	out := re.ReplaceAllString(string(data), replacement)
	return os.WriteFile(path, []byte(out), 0o644)
}

// ConfigureBaseSystem sets hostname/timezone/keymap/locale for both init
// systems (port of configure_base_system).
func ConfigureBaseSystem(c *Context) error {
	s := &c.Cfg.System

	if c.Cfg.UsesMusl() {
		c.R.log("Installing musl-locales")
		if err := c.R.Try("emerge", "--verbose", "sys-apps/musl-locales"); err != nil {
			return err
		}
		if err := appendFile(c, "/etc/env.d/00local",
			`MUSL_LOCPATH="/usr/share/i18n/locales/musl"`); err != nil {
			return err
		}
	} else {
		c.R.log("Generating locales")
		if err := os.WriteFile("/etc/locale.gen",
			[]byte(strings.Join(s.Locales, "\n")+"\n"), 0o644); err != nil {
			return fmt.Errorf("could not write /etc/locale.gen: %w", err)
		}
		if err := c.R.Try("locale-gen"); err != nil {
			return fmt.Errorf("could not generate locales: %w", err)
		}
	}

	if c.Cfg.UsesSystemd() {
		c.R.log("Setting machine-id")
		if err := c.R.Try("systemd-machine-id-setup"); err != nil {
			return err
		}
		c.R.log("Selecting hostname")
		if err := os.WriteFile("/etc/hostname", []byte(s.Hostname+"\n"), 0o644); err != nil {
			return fmt.Errorf("could not write /etc/hostname: %w", err)
		}
		c.R.log("Selecting keymap")
		if err := os.WriteFile("/etc/vconsole.conf",
			[]byte("KEYMAP="+s.Keymap+"\n"), 0o644); err != nil {
			return fmt.Errorf("could not write /etc/vconsole.conf: %w", err)
		}
		c.R.log("Selecting locale")
		if err := os.WriteFile("/etc/locale.conf",
			[]byte("LANG="+s.Locale+"\n"), 0o644); err != nil {
			return fmt.Errorf("could not write /etc/locale.conf: %w", err)
		}
		c.R.log("Selecting timezone")
		if err := os.MkdirAll("/etc", 0o755); err != nil {
			return err
		}
		if out, err := c.R.QuietRun("ln", "-sfn",
			"../usr/share/zoneinfo/"+s.Timezone, "/etc/localtime"); err != nil {
			return fmt.Errorf("could not change /etc/localtime link:\n%s", out)
		}
	} else {
		c.R.log("Selecting hostname")
		if err := replaceLine("/etc/conf.d/hostname", "hostname=",
			fmt.Sprintf("hostname=%q", s.Hostname)); err != nil {
			return fmt.Errorf("could not sed replace in /etc/conf.d/hostname: %w", err)
		}

		if c.Cfg.UsesMusl() {
			if err := c.R.Try("emerge", "-v", "sys-libs/timezone-data"); err != nil {
				return err
			}
			c.R.log("Selecting timezone")
			if err := appendFile(c, "/etc/env.d/00local",
				fmt.Sprintf("TZ=%q", s.Timezone)); err != nil {
				return err
			}
		} else {
			c.R.log("Selecting timezone")
			if err := os.WriteFile("/etc/timezone", []byte(s.Timezone+"\n"), 0o644); err != nil {
				return fmt.Errorf("could not write /etc/timezone: %w", err)
			}
			if err := os.Chmod("/etc/timezone", 0o644); err != nil {
				return fmt.Errorf("could not set correct permissions for /etc/timezone: %w", err)
			}
			if err := c.R.Try("emerge", "-v", "--config", "sys-libs/timezone-data"); err != nil {
				return err
			}
		}

		c.R.log("Selecting keymap")
		if err := replaceLine("/etc/conf.d/keymaps", "keymap=",
			fmt.Sprintf("keymap=%q", s.Keymap)); err != nil {
			return fmt.Errorf("could not sed replace in /etc/conf.d/keymaps: %w", err)
		}

		c.R.log("Selecting locale")
		if err := c.R.Try("eselect", "locale", "set", s.Locale); err != nil {
			return err
		}
	}

	c.R.log("Updating environment")
	if err := c.R.Try("env-update"); err != nil {
		return fmt.Errorf("error in env-update: %w", err)
	}
	setUmask(0o077)
	return nil
}

// ConfigurePortage prepares /etc/portage and mirrors
// (port of configure_portage).
func ConfigurePortage(c *Context) error {
	for dir := range map[string]bool{
		"/etc/portage/package.use":      true,
		"/etc/portage/package.keywords": true,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("could not create %s: %w", dir, err)
		}
	}
	touch := func(path string) error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		return f.Close()
	}
	if err := touch("/etc/portage/package.use/zz-autounmask"); err != nil {
		return err
	}
	if err := touch("/etc/portage/package.keywords/zz-autounmask"); err != nil {
		return err
	}
	if err := touch("/etc/portage/package.license"); err != nil {
		return err
	}

	for _, line := range c.Cfg.Packages.UseFlags {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := appendFile(c, "/etc/portage/package.use/user", line); err != nil {
			return fmt.Errorf("could not write /etc/portage/package.use/user: %w", err)
		}
	}

	if err := appendFile(c, "/etc/portage/make.conf",
		fmt.Sprintf("MAKEOPTS=\"-j%d\"", c.NProc)); err != nil {
		return fmt.Errorf("could not modify /etc/portage/make.conf: %w", err)
	}

	g := &c.Cfg.Gentoo
	if g.SelectMirrors {
		c.R.log("Temporarily installing mirrorselect")
		if err := c.R.Try("emerge", "--verbose", "--oneshot",
			"app-portage/mirrorselect"); err != nil {
			return err
		}
		c.R.log("Selecting fastest portage mirrors")
		params := []string{"-s", "4", "-b", "10"}
		if g.SelectMirrorsLargeFile {
			params = append(params, "-D")
		}
		if err := c.R.Try("mirrorselect", params...); err != nil {
			return err
		}
	}

	if c.Cfg.Packages.EnableBinpkg {
		if err := appendFile(c, "/etc/portage/make.conf",
			`FEATURES="getbinpkg binpkg-request-signature"`); err != nil {
			return err
		}
		if err := c.R.Try("getuto"); err != nil {
			return err
		}
		if out, err := c.R.QuietRun("chmod", "644", "/etc/portage/gnupg/pubring.kbx"); err != nil {
			return fmt.Errorf("chmod pubring.kbx failed:\n%s", out)
		}
	}

	if err := appendMakeConfUser(c); err != nil {
		return err
	}

	if err := os.Chmod("/etc/portage/make.conf", 0o644); err != nil {
		return fmt.Errorf("could not chmod 644 /etc/portage/make.conf: %w", err)
	}
	return nil
}

// appendMakeConfUser appends the make.conf options and freeform extra content
// selected in the [makeconf] section of gentoo.toml.
func appendMakeConfUser(c *Context) error {
	path := "/etc/portage/make.conf"
	arch := c.Cfg.Gentoo.Arch
	jobs := fmt.Sprintf("%d", c.NProc)

	for _, key := range c.Cfg.MakeConf.Options {
		o := config.LookupMakeConfOption(key)
		if o == nil {
			continue
		}
		line := strings.ReplaceAll(o.Line, "${JOBS}", jobs)
		line = strings.ReplaceAll(line, "${ARCH}", arch)
		if err := appendFile(c, path, line); err != nil {
			return fmt.Errorf("could not modify /etc/portage/make.conf: %w", err)
		}
	}

	extra := strings.TrimSpace(c.Cfg.MakeConf.Extra)
	if extra != "" {
		if err := appendFile(c, path, extra); err != nil {
			return fmt.Errorf("could not modify /etc/portage/make.conf: %w", err)
		}
	}
	return nil
}

// ConfigureGitSync writes repos.conf for git sync and runs emerge --sync.
func ConfigureGitSync(c *Context) error {
	if c.Cfg.Gentoo.PortageSyncType != "git" {
		return nil
	}
	g := &c.Cfg.Gentoo
	if err := os.MkdirAll("/etc/portage/repos.conf", 0o755); err != nil {
		return err
	}
	depth := 1
	if g.PortageGitFullHistory {
		depth = 0
	}
	content := fmt.Sprintf(`[DEFAULT]
main-repo = gentoo

[gentoo]
location = /var/db/repos/gentoo
sync-type = git
sync-uri = %s
auto-sync = yes
sync-depth = %d
sync-git-verify-commit-signature = yes
sync-openpgp-key-path = /usr/share/openpgp-keys/gentoo-release.asc
`, g.PortageGitMirror, depth)
	path := "/etc/portage/repos.conf/gentoo.conf"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("could not write '%s': %w", path, err)
	}
	if err := os.RemoveAll("/var/db/repos/gentoo"); err != nil {
		return fmt.Errorf("could not delete obsolete rsync gentoo repository: %w", err)
	}
	return c.R.Try("emerge", "--sync")
}

// EnableRepositories enables the user-selected third-party overlays using
// eselect repository. The main "gentoo" repo is always present and is not
// touched here. Returns immediately when no overlays were selected.
func EnableRepositories(c *Context) error {
	sel := c.Cfg.Packages.EnablingRepos
	sel = append([]string{}, sel...)
	if len(sel) == 0 {
		return nil
	}

	c.R.log("Installing eselect-repository")
	if err := c.R.Try("emerge", "--quiet", "app-eselect/eselect-repository"); err != nil {
		return err
	}

	c.R.log("Enabling selected repositories")
	if err := os.MkdirAll("/etc/portage/repos.conf", 0o755); err != nil {
		return err
	}
	args := append([]string{"repository", "enable"}, sel...)
	if err := c.R.Try("eselect", args...); err != nil {
		return fmt.Errorf("could not enable repositories %v: %w", sel, err)
	}

	c.R.log("Syncing enabled repositories")
	for _, name := range sel {
		if err := c.R.Try("emaint", "sync", "-r", name); err != nil {
			return fmt.Errorf("could not sync repository %q: %w", name, err)
		}
	}
	return nil
}

// MainInstallGentooInChroot is the full in-chroot installation sequence
// (port of main_install_gentoo_in_chroot).
func MainInstallGentooInChroot(c *Context) error {
	c.R.log("Clearing root password")
	if err := c.R.Try("passwd", "-d", "root"); err != nil {
		return fmt.Errorf("could not change root password: %w", err)
	}

	c.R.log("Syncing portage tree")
	if err := c.R.Try("emerge-webrsync"); err != nil {
		return err
	}

	if c.Layout.Flags.UsedRaid {
		c.R.log("Installing mdadm")
		if err := c.R.Try("emerge", "--verbose", "sys-fs/mdadm"); err != nil {
			return err
		}
	}

	if c.IsEFI() {
		if err := MountEfiVars(c); err != nil {
			return err
		}
		c.R.log("Mounting efi partition")
		if err := MountByID(c, c.Layout.EFIID, "/boot/efi"); err != nil {
			return err
		}
	} else {
		c.R.log("Mounting bios partition")
		if err := MountByID(c, c.Layout.BIOSID, "/boot/bios"); err != nil {
			return err
		}
	}

	if err := ConfigureBaseSystem(c); err != nil {
		return err
	}
	if err := ConfigurePortage(c); err != nil {
		return err
	}

	c.R.log("Installing git")
	if err := c.R.Try("emerge", "--verbose", "dev-vcs/git"); err != nil {
		return err
	}
	if err := ConfigureGitSync(c); err != nil {
		return err
	}

	c.R.log("Generating ssh host keys")
	if err := c.R.Try("ssh-keygen", "-A"); err != nil {
		return err
	}

	// Before dracut, which might need them for remote unlocking.
	if err := InstallAuthorizedKeys(c); err != nil {
		return err
	}

	c.R.log("Enabling dracut USE flag on sys-kernel/installkernel")
	if err := os.MkdirAll("/etc/portage/package.use", 0o755); err != nil {
		return err
	}
	if err := os.WriteFile("/etc/portage/package.use/installkernel",
		[]byte("sys-kernel/installkernel dracut\n"), 0o644); err != nil {
		return fmt.Errorf("could not write /etc/portage/package.use/installkernel: %w", err)
	}

	if c.Cfg.Packages.KernelType == "source" && c.Cfg.Packages.KernelDeblob {
		c.R.log("Enabling deblob USE flag on sys-kernel/gentoo-kernel")
		if err := os.WriteFile("/etc/portage/package.use/gentoo-kernel",
			[]byte("sys-kernel/gentoo-kernel deblob\n"), 0o644); err != nil {
			return fmt.Errorf("could not write /etc/portage/package.use/gentoo-kernel: %w", err)
		}
	}

	if c.Cfg.Packages.KernelType == "source" {
		c.R.log("Building kernel from source (sys-kernel/gentoo-kernel)")
		if err := c.R.Try("emerge", "--verbose",
			"sys-kernel/dracut", "sys-kernel/gentoo-kernel", "app-arch/zstd"); err != nil {
			return err
		}
	} else {
		c.R.log("Installing binary kernel (sys-kernel/gentoo-kernel-bin)")
		if err := c.R.Try("emerge", "--verbose",
			"sys-kernel/dracut", "sys-kernel/gentoo-kernel-bin", "app-arch/zstd"); err != nil {
			return err
		}
	}

	if c.Layout.Flags.UsedLuks {
		c.R.log("Installing cryptsetup")
		if err := c.R.Try("emerge", "--verbose", "sys-fs/cryptsetup"); err != nil {
			return err
		}
		if c.Cfg.UsesSystemd() {
			c.R.log("Enabling cryptsetup USE flag on sys-apps/systemd")
			if err := os.WriteFile("/etc/portage/package.use/systemd",
				[]byte("sys-apps/systemd cryptsetup\n"), 0o644); err != nil {
				return fmt.Errorf("could not write /etc/portage/package.use/systemd: %w", err)
			}
			c.R.log("Rebuilding systemd with changed USE flag")
			if err := c.R.Try("emerge", "--verbose", "--changed-use",
				"--oneshot", "sys-apps/systemd"); err != nil {
				return err
			}
		}
	}

	if c.Layout.Flags.UsedBtrfs {
		c.R.log("Installing btrfs-progs")
		if err := c.R.Try("emerge", "--verbose", "sys-fs/btrfs-progs"); err != nil {
			return err
		}
	}

	if err := c.R.Try("emerge", "--verbose", "dev-vcs/git"); err != nil {
		return err
	}

	if c.Layout.Flags.UsedZFS {
		c.R.log("Installing zfs")
		if err := c.R.Try("emerge", "--verbose", "sys-fs/zfs", "sys-fs/zfs-kmod"); err != nil {
			return err
		}
		c.R.log("Enabling zfs services")
		if c.Cfg.UsesSystemd() {
			for _, svc := range []string{"zfs.target", "zfs-import-cache",
				"zfs-mount", "zfs-import.target"} {
				if err := c.R.Try("systemctl", "enable", svc); err != nil {
					return err
				}
			}
		} else {
			for _, svc := range []string{"zfs-import", "zfs-mount"} {
				if err := c.R.Try("rc-update", "add", svc, "boot"); err != nil {
					return err
				}
			}
		}
	}

	c.R.log("Installing kernel")
	if err := InstallKernel(c); err != nil {
		return err
	}
	if err := GenerateFstab(c); err != nil {
		return err
	}

	c.R.log("Installing gentoolkit")
	if err := c.R.Try("emerge", "--verbose", "app-portage/gentoolkit"); err != nil {
		return err
	}

	if err := ConfigureNetworking(c); err != nil {
		return err
	}
	if err := EnableRepositories(c); err != nil {
		return err
	}
	if c.Cfg.Packages.EnableSSHD {
		if err := EnableSSHD(c); err != nil {
			return err
		}
	}

	profilePkgs := c.Cfg.ProfilePackages()
	if len(profilePkgs) > 0 {
		c.R.log("Installing packages from the selected profile")
		args := append([]string{"--verbose", "--autounmask-continue=y", "--"},
			profilePkgs...)
		if err := c.R.Try("emerge", args...); err != nil {
			return err
		}
	}

	extra := append(append([]string{}, c.Cfg.Packages.Additional...),
		c.Cfg.Packages.CustomPackages...)
	if len(extra) > 0 {
		c.R.log("Installing additional packages")
		args := append([]string{"--verbose", "--autounmask-continue=y", "--"},
			extra...)
		if err := c.R.Try("emerge", args...); err != nil {
			return err
		}
	}

	setRootPw, err := AskYesNo(c.R, "Do you want to assign a root password now?", false)
	if err != nil {
		return err
	}
	if setRootPw {
		if err := c.R.Try("passwd", "root"); err != nil {
			return err
		}
		c.R.log("Root password assigned")
	} else {
		if err := c.R.Try("passwd", "-d", "root"); err != nil {
			return err
		}
		c.R.log("[!] Root password cleared, set one as soon as possible!")
	}

	if c.Cfg.Gentoo.UsePortageTesting {
		c.R.logf("Adding ~%s to ACCEPT_KEYWORDS", c.Cfg.Gentoo.Arch)
		if err := appendFile(c, "/etc/portage/make.conf",
			fmt.Sprintf("ACCEPT_KEYWORDS=\"~%s\"", c.Cfg.Gentoo.Arch)); err != nil {
			return fmt.Errorf("could not modify /etc/portage/make.conf: %w", err)
		}
	}

	c.R.log("Gentoo installation complete.")
	if c.Layout.Flags.UsedLuks {
		c.R.logf("A backup of your luks headers can be found at '%s', in case you want to have a backup.",
			LuksHeaderBackupDir)
	}
	c.R.logf("You may now reboot your system or execute 'gentooinstall chroot %s' to enter your system in a chroot.",
		RootMountpoint)
	return nil
}

// Ensure /etc exists early for systems whose stage3 lacks it (defensive).
var _ = filepath.Join
