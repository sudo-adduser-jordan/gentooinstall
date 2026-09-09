package installer

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"

	"gentooinstall/lib/config"
)

// SetupChrootEnv applies the environment from dispatch_chroot.sh.
func SetupChrootEnv(ctx *Context) {
	setUmask(0o077)
	nproc := runtime.NumCPU()
	if nproc < 2 {
		nproc = 2 // parity with nproc || echo 2 edge cases
	}
	ctx.NProc = nproc
	_ = os.Setenv("NPROC", fmt.Sprint(nproc))
	_ = os.Setenv("NPROC_ONE", fmt.Sprint(nproc+1))
	_ = os.Setenv("MAKEFLAGS", "-j"+fmt.Sprint(nproc))
	_ = os.Setenv("EMERGE_DEFAULT_OPTS",
		fmt.Sprintf("--jobs=%d --load-average=%d", nproc+1, nproc))
}

func (ctx *Context) replaceLine(path, matchPrefix, replacement string) error {
	data, err := ctx.readFile(path)
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`(?m)^.*` + regexp.QuoteMeta(matchPrefix) + `.*$`)
	out := re.ReplaceAllString(string(data), replacement)
	return ctx.writeFile(path, []byte(out), 0o644)
}

// ConfigureBaseSystem sets hostname/timezone/keymap/locale for both init
// systems (port of configure_base_system).
func ConfigureBaseSystem(ctx *Context) error {
	system := &ctx.Cfg.System

	if ctx.Cfg.UsesMusl() {
		ctx.Runner.log("Installing musl-locales")
		if err := ctx.Runner.Try("emerge", "--verbose", "sys-apps/musl-locales"); err != nil {
			return err
		}
		if err := ctx.appendFile("/etc/env.d/00local",
			`MUSL_LOCPATH="/usr/share/i18n/locales/musl"`); err != nil {
			return err
		}
	} else {
		ctx.Runner.log("Generating locales")
		if err := ctx.writeFile("/etc/locale.gen",
			[]byte(strings.Join(system.Locales, "\n")+"\n"), 0o644); err != nil {
			return fmt.Errorf("could not write /etc/locale.gen: %w", err)
		}
		if err := ctx.Runner.Try("locale-gen"); err != nil {
			return fmt.Errorf("could not generate locales: %w", err)
		}
	}

	if ctx.Cfg.UsesSystemd() {
		ctx.Runner.log("Setting machine-id")
		if err := ctx.Runner.Try("systemd-machine-id-setup"); err != nil {
			return err
		}
		ctx.Runner.log("Selecting hostname")
		if err := ctx.writeFile("/etc/hostname", []byte(system.Hostname+"\n"), 0o644); err != nil {
			return fmt.Errorf("could not write /etc/hostname: %w", err)
		}
		ctx.Runner.log("Selecting keymap")
		if err := ctx.writeFile("/etc/vconsole.conf",
			[]byte("KEYMAP="+system.Keymap+"\n"), 0o644); err != nil {
			return fmt.Errorf("could not write /etc/vconsole.conf: %w", err)
		}
		ctx.Runner.log("Selecting locale")
		if err := ctx.writeFile("/etc/locale.conf",
			[]byte("LANG="+system.Locale+"\n"), 0o644); err != nil {
			return fmt.Errorf("could not write /etc/locale.conf: %w", err)
		}
		ctx.Runner.log("Selecting timezone")
		if err := ctx.mkdirAll("/etc", 0o755); err != nil {
			return err
		}
		if out, err := ctx.Runner.QuietRun("ln", "-sfn",
			"../usr/share/zoneinfo/"+system.Timezone, "/etc/localtime"); err != nil {
			return fmt.Errorf("could not change /etc/localtime link:\n%s", out)
		}
	} else {
		ctx.Runner.log("Selecting hostname")
		if err := ctx.replaceLine("/etc/conf.d/hostname", "hostname=",
			fmt.Sprintf("hostname=%q", system.Hostname)); err != nil {
			return fmt.Errorf("could not sed replace in /etc/conf.d/hostname: %w", err)
		}

		if ctx.Cfg.UsesMusl() {
			if err := ctx.Runner.Try("emerge", "-v", "sys-libs/timezone-data"); err != nil {
				return err
			}
			ctx.Runner.log("Selecting timezone")
			if err := ctx.appendFile("/etc/env.d/00local",
				fmt.Sprintf("TZ=%q", system.Timezone)); err != nil {
				return err
			}
		} else {
			ctx.Runner.log("Selecting timezone")
			if err := ctx.writeFile("/etc/timezone", []byte(system.Timezone+"\n"), 0o644); err != nil {
				return fmt.Errorf("could not write /etc/timezone: %w", err)
			}
			if err := ctx.chmod("/etc/timezone", 0o644); err != nil {
				return fmt.Errorf("could not set correct permissions for /etc/timezone: %w", err)
			}
			if err := ctx.Runner.Try("emerge", "-v", "--config", "sys-libs/timezone-data"); err != nil {
				return err
			}
		}

		ctx.Runner.log("Selecting keymap")
		if err := ctx.replaceLine("/etc/conf.d/keymaps", "keymap=",
			fmt.Sprintf("keymap=%q", system.Keymap)); err != nil {
			return fmt.Errorf("could not sed replace in /etc/conf.d/keymaps: %w", err)
		}

		ctx.Runner.log("Selecting locale")
		if err := ctx.Runner.Try("eselect", "locale", "set", system.Locale); err != nil {
			return err
		}
	}

	ctx.Runner.log("Updating environment")
	if err := ctx.Runner.Try("env-update"); err != nil {
		return fmt.Errorf("error in env-update: %w", err)
	}
	setUmask(0o077)
	return nil
}

// ConfigurePortage prepares /etc/portage and mirrors
// (port of configure_portage).
func ConfigurePortage(ctx *Context) error {
	for dir := range map[string]bool{
		"/etc/portage/package.use":      true,
		"/etc/portage/package.keywords": true,
	} {
		if err := ctx.mkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("could not create %s: %w", dir, err)
		}
	}
	touch := func(path string) error {
		return ctx.touchFile(path)
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

	for _, line := range ctx.Cfg.Packages.UseFlags {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := ctx.appendFile("/etc/portage/package.use/user", line); err != nil {
			return fmt.Errorf("could not write /etc/portage/package.use/user: %w", err)
		}
	}

	if err := ctx.appendFile("/etc/portage/make.conf",
		fmt.Sprintf("MAKEOPTS=\"-j%d\"", ctx.NProc)); err != nil {
		return fmt.Errorf("could not modify /etc/portage/make.conf: %w", err)
	}

	gentoo := &ctx.Cfg.Gentoo
	if gentoo.SelectMirrors {
		ctx.Runner.log("Temporarily installing mirrorselect")
		if err := ctx.Runner.Try("emerge", "--verbose", "--oneshot",
			"app-portage/mirrorselect"); err != nil {
			return err
		}
		ctx.Runner.log("Selecting fastest portage mirrors")
		params := []string{"-s", "4", "-b", "10"}
		if gentoo.SelectMirrorsLargeFile {
			params = append(params, "-D")
		}
		if err := ctx.Runner.Try("mirrorselect", params...); err != nil {
			return err
		}
	}

	if ctx.Cfg.Packages.EnableBinpkg {
		if err := ctx.appendFile("/etc/portage/make.conf",
			`FEATURES="getbinpkg binpkg-request-signature"`); err != nil {
			return err
		}
		if err := ctx.Runner.Try("getuto"); err != nil {
			return err
		}
		if out, err := ctx.Runner.QuietRun("chmod", "644", "/etc/portage/gnupg/pubring.kbx"); err != nil {
			return fmt.Errorf("chmod pubring.kbx failed:\n%s", out)
		}
	}

	if err := appendMakeConfUser(ctx); err != nil {
		return err
	}

	if err := ctx.chmod("/etc/portage/make.conf", 0o644); err != nil {
		return fmt.Errorf("could not chmod 644 /etc/portage/make.conf: %w", err)
	}
	return nil
}

// appendMakeConfUser appends the make.conf options and freeform extra content
// selected in the [makeconf] section of gentoo.toml.
func appendMakeConfUser(ctx *Context) error {
	path := "/etc/portage/make.conf"
	arch := ctx.Cfg.Gentoo.Arch
	jobs := fmt.Sprintf("%d", ctx.NProc)

	for _, key := range ctx.Cfg.MakeConf.Options {
		option := config.LookupMakeConfOption(key)
		if option == nil {
			continue
		}
		line := strings.ReplaceAll(option.Line, "${JOBS}", jobs)
		line = strings.ReplaceAll(line, "${ARCH}", arch)
		if err := ctx.appendFile(path, line); err != nil {
			return fmt.Errorf("could not modify /etc/portage/make.conf: %w", err)
		}
	}

	extra := strings.TrimSpace(ctx.Cfg.MakeConf.Extra)
	if extra != "" {
		if err := ctx.appendFile(path, extra); err != nil {
			return fmt.Errorf("could not modify /etc/portage/make.conf: %w", err)
		}
	}
	return nil
}

// reposConfContent returns the /etc/portage/repos.conf/gentoo.conf content
// for the configured portage sync type (git or rsync). The "gentoo" repo must
// be registered before any sync (emerge-webrsync or emerge --sync), otherwise
// portage fails with "Repository 'gentoo' not found".
func reposConfContent(ctx *Context) string {
	return reposConfContentFor(ctx, ctx.Cfg.Gentoo.PortageSyncType)
}

// reposConfContentFor renders the repos.conf content for an explicit sync
// type instead of the configured one, so the install sequence can register
// an rsync-typed repo for the emerge-webrsync seed phase even when the
// configured type is git (webrsync rejects git-typed repos with an
// "invalid sync type" validation failure).
func reposConfContentFor(ctx *Context, syncType string) string {
	gentoo := &ctx.Cfg.Gentoo
	if syncType == "rsync" {
		uri := gentoo.PortageRsyncMirror
		if uri == "" {
			uri = config.DefaultPortageRsyncMirror
		}
		return fmt.Sprintf(`[DEFAULT]
main-repo = gentoo

[gentoo]
location = /var/db/repos/gentoo
sync-type = rsync
sync-uri = %s
auto-sync = yes
`, uri)
	}
	depth := 1
	if gentoo.PortageGitFullHistory {
		depth = 0
	}
	return fmt.Sprintf(`[DEFAULT]
main-repo = gentoo

[gentoo]
location = /var/db/repos/gentoo
sync-type = git
sync-uri = %s
auto-sync = yes
sync-depth = %d
sync-git-verify-commit-signature = yes
sync-openpgp-key-path = /usr/share/openpgp-keys/gentoo-release.asc
`, gentoo.PortageGitMirror, depth)
}

// WriteReposConf registers the "gentoo" repository in
// /etc/portage/repos.conf so portage tools (emerge-webrsync, emerge --sync,
// emaint) can resolve it. The repository location is pre-created as well;
// portage reports "Repository 'gentoo' not found" when a configured repo has
// no usable location, which otherwise breaks the initial emerge-webrsync.
func WriteReposConf(ctx *Context) error {
	return writeReposConf(ctx, ctx.Cfg.Gentoo.PortageSyncType)
}

func writeReposConf(ctx *Context, syncType string) error {
	if err := ctx.mkdirAll("/etc/portage/repos.conf", 0o755); err != nil {
		return err
	}
	path := "/etc/portage/repos.conf/gentoo.conf"
	if err := ctx.writeFile(path, []byte(reposConfContentFor(ctx, syncType)), 0o644); err != nil {
		return fmt.Errorf("could not write '%s': %w", path, err)
	}
	if err := ctx.mkdirAll("/var/db/repos/gentoo", 0o755); err != nil {
		return fmt.Errorf("could not create repository location: %w", err)
	}
	return nil
}

// SeedPortageTree registers the "gentoo" repository rsync-typed and
// populates it with emerge-webrsync. The seed always uses the rsync variant,
// even when the configured sync type is git: emerge-webrsync rejects
// git-typed repos, and the seed tree is what lets the later
// `emerge dev-vcs/git` succeed before the real git sync. A stale .git
// checkout left by a previous git-typed run is dropped first so the seed
// never layers a snapshot over a git working tree.
func SeedPortageTree(ctx *Context) error {
	ctx.Runner.log("Registering gentoo repository in repos.conf")
	if err := writeReposConf(ctx, "rsync"); err != nil {
		return err
	}
	if err := ctx.removeAll("/var/db/repos/gentoo/.git"); err != nil {
		return fmt.Errorf("could not clear stale git checkout: %w", err)
	}
	ctx.Runner.log("Syncing portage tree")
	return ctx.Runner.Try("emerge-webrsync")
}

// ConfigureGitSync re-syncs the portage tree over git for the git sync type.
// The rsync-typed seed registration is handled by SeedPortageTree, which runs
// before the initial emerge-webrsync; for rsync this function is a no-op.
func ConfigureGitSync(ctx *Context) error {
	if ctx.Cfg.Gentoo.PortageSyncType != "git" {
		return nil
	}
	if err := ctx.mkdirAll("/etc/portage/repos.conf", 0o755); err != nil {
		return err
	}
	path := "/etc/portage/repos.conf/gentoo.conf"
	if err := ctx.writeFile(path, []byte(reposConfContent(ctx)), 0o644); err != nil {
		return fmt.Errorf("could not write '%s': %w", path, err)
	}
	if err := ctx.removeAll("/var/db/repos/gentoo"); err != nil {
		return fmt.Errorf("could not delete obsolete rsync gentoo repository: %w", err)
	}
	return ctx.Runner.Try("emerge", "--sync")
}

// EnableRepositories enables the user-selected third-party overlays using
// eselect repository. The main "gentoo" repo is always present and is not
// touched here. Returns immediately when no overlays were selected.
func EnableRepositories(ctx *Context) error {
	sel := ctx.Cfg.Packages.EnablingRepos
	sel = append([]string{}, sel...)
	if len(sel) == 0 {
		return nil
	}

	ctx.Runner.log("Installing eselect-repository")
	if err := ctx.Runner.Try("emerge", "--quiet", "app-eselect/eselect-repository"); err != nil {
		return err
	}

	ctx.Runner.log("Enabling selected repositories")
	if err := ctx.mkdirAll("/etc/portage/repos.conf", 0o755); err != nil {
		return err
	}
	args := append([]string{"repository", "enable"}, sel...)
	if err := ctx.Runner.Try("eselect", args...); err != nil {
		return fmt.Errorf("could not enable repositories %v: %w", sel, err)
	}

	ctx.Runner.log("Syncing enabled repositories")
	for _, name := range sel {
		if err := ctx.Runner.Try("emaint", "sync", "-r", name); err != nil {
			return fmt.Errorf("could not sync repository %q: %w", name, err)
		}
	}
	return nil
}

// MainInstallGentooInChroot is the full in-chroot installation sequence
// (port of main_install_gentoo_in_chroot).
func MainInstallGentooInChroot(ctx *Context) error {
	ctx.Runner.log("Clearing root password")
	if err := ctx.Runner.Try("passwd", "-d", "root"); err != nil {
		return fmt.Errorf("could not change root password: %w", err)
	}

	if err := SeedPortageTree(ctx); err != nil {
		return err
	}

	if ctx.Layout.Flags.UsedRaid {
		ctx.Runner.log("Installing mdadm")
		if err := ctx.Runner.Try("emerge", "--verbose", "sys-fs/mdadm"); err != nil {
			return err
		}
	}

	if ctx.IsEFI() {
		if err := MountEfiVars(ctx); err != nil {
			return err
		}
		ctx.Runner.log("Mounting efi partition")
		if err := MountByID(ctx, ctx.Layout.EFIID, "/boot/efi"); err != nil {
			return err
		}
	} else {
		ctx.Runner.log("Mounting bios partition")
		if err := MountByID(ctx, ctx.Layout.BIOSID, "/boot/bios"); err != nil {
			return err
		}
	}

	if err := ConfigureBaseSystem(ctx); err != nil {
		return err
	}
	if err := ConfigurePortage(ctx); err != nil {
		return err
	}

	ctx.Runner.log("Installing git")
	if err := ctx.Runner.Try("emerge", "--verbose", "dev-vcs/git"); err != nil {
		return err
	}
	if err := ConfigureGitSync(ctx); err != nil {
		return err
	}

	ctx.Runner.log("Generating ssh host keys")
	if err := ctx.Runner.Try("ssh-keygen", "-A"); err != nil {
		return err
	}

	// Before dracut, which might need them for remote unlocking.
	if err := InstallAuthorizedKeys(ctx); err != nil {
		return err
	}

	ctx.Runner.log("Enabling dracut USE flag on sys-kernel/installkernel")
	if err := ctx.mkdirAll("/etc/portage/package.use", 0o755); err != nil {
		return err
	}
	if err := ctx.writeFile("/etc/portage/package.use/installkernel",
		[]byte("sys-kernel/installkernel dracut\n"), 0o644); err != nil {
		return fmt.Errorf("could not write /etc/portage/package.use/installkernel: %w", err)
	}

	if ctx.Cfg.Packages.KernelType == "source" && ctx.Cfg.Packages.KernelDeblob {
		ctx.Runner.log("Enabling deblob USE flag on sys-kernel/gentoo-kernel")
		if err := ctx.writeFile("/etc/portage/package.use/gentoo-kernel",
			[]byte("sys-kernel/gentoo-kernel deblob\n"), 0o644); err != nil {
			return fmt.Errorf("could not write /etc/portage/package.use/gentoo-kernel: %w", err)
		}
	}

	if ctx.Cfg.Packages.KernelType == "source" {
		ctx.Runner.log("Building kernel from source (sys-kernel/gentoo-kernel)")
		if err := ctx.Runner.Try("emerge", "--verbose",
			"sys-kernel/dracut", "sys-kernel/gentoo-kernel", "app-arch/zstd"); err != nil {
			return err
		}
	} else {
		ctx.Runner.log("Installing binary kernel (sys-kernel/gentoo-kernel-bin)")
		if err := ctx.Runner.Try("emerge", "--verbose",
			"sys-kernel/dracut", "sys-kernel/gentoo-kernel-bin", "app-arch/zstd"); err != nil {
			return err
		}
	}

	if ctx.Layout.Flags.UsedLuks {
		ctx.Runner.log("Installing cryptsetup")
		if err := ctx.Runner.Try("emerge", "--verbose", "sys-fs/cryptsetup"); err != nil {
			return err
		}
		if ctx.Cfg.UsesSystemd() {
			ctx.Runner.log("Enabling cryptsetup USE flag on sys-apps/systemd")
			if err := ctx.writeFile("/etc/portage/package.use/systemd",
				[]byte("sys-apps/systemd cryptsetup\n"), 0o644); err != nil {
				return fmt.Errorf("could not write /etc/portage/package.use/systemd: %w", err)
			}
			ctx.Runner.log("Rebuilding systemd with changed USE flag")
			if err := ctx.Runner.Try("emerge", "--verbose", "--changed-use",
				"--oneshot", "sys-apps/systemd"); err != nil {
				return err
			}
		}
	}

	if ctx.Layout.Flags.UsedBtrfs {
		ctx.Runner.log("Installing btrfs-progs")
		if err := ctx.Runner.Try("emerge", "--verbose", "sys-fs/btrfs-progs"); err != nil {
			return err
		}
	}

	if ctx.Layout.Flags.UsedZFS {
		ctx.Runner.log("Installing zfs")
		if err := ctx.Runner.Try("emerge", "--verbose", "sys-fs/zfs", "sys-fs/zfs-kmod"); err != nil {
			return err
		}
		ctx.Runner.log("Enabling zfs services")
		if ctx.Cfg.UsesSystemd() {
			for _, svc := range []string{"zfs.target", "zfs-import-cache",
				"zfs-mount", "zfs-import.target"} {
				if err := ctx.Runner.Try("systemctl", "enable", svc); err != nil {
					return err
				}
			}
		} else {
			for _, svc := range []string{"zfs-import", "zfs-mount"} {
				if err := ctx.Runner.Try("rc-update", "add", svc, "boot"); err != nil {
					return err
				}
			}
		}
	}

	ctx.Runner.log("Installing kernel")
	if err := InstallKernel(ctx); err != nil {
		return err
	}
	if err := GenerateFstab(ctx); err != nil {
		return err
	}

	ctx.Runner.log("Installing gentoolkit")
	if err := ctx.Runner.Try("emerge", "--verbose", "app-portage/gentoolkit"); err != nil {
		return err
	}

	if err := ConfigureNetworking(ctx); err != nil {
		return err
	}
	if err := EnableRepositories(ctx); err != nil {
		return err
	}
	if ctx.Cfg.Packages.EnableSSHD {
		if err := EnableSSHD(ctx); err != nil {
			return err
		}
	}

	profilePkgs := ctx.Cfg.ProfilePackages()
	if len(profilePkgs) > 0 {
		ctx.Runner.log("Installing packages from the selected profile")
		args := append([]string{"--verbose", "--autounmask-continue=y", "--"},
			profilePkgs...)
		if err := ctx.Runner.Try("emerge", args...); err != nil {
			return err
		}
	}

	extra := append(append([]string{}, ctx.Cfg.Packages.Additional...),
		ctx.Cfg.Packages.CustomPackages...)
	if len(extra) > 0 {
		ctx.Runner.log("Installing additional packages")
		args := append([]string{"--verbose", "--autounmask-continue=y", "--"},
			extra...)
		if err := ctx.Runner.Try("emerge", args...); err != nil {
			return err
		}
	}

	setRootPw, err := AskYesNo(ctx.Runner, "Do you want to assign a root password now?", false)
	if err != nil {
		return err
	}
	if setRootPw {
		if err := ctx.Runner.Try("passwd", "root"); err != nil {
			return err
		}
		ctx.Runner.log("Root password assigned")
	} else {
		if err := ctx.Runner.Try("passwd", "-d", "root"); err != nil {
			return err
		}
		ctx.Runner.log("[!] Root password cleared, set one as soon as possible!")
	}

	// If configured, change to gentoo testing at the last moment, matching
	// upstream: this keeps the installation itself on stable so testing
	// blockers cannot break it. Deal with the blockers after installation.
	if ctx.Cfg.Gentoo.UsePortageTesting {
		ctx.Runner.logf("Adding ~%s to ACCEPT_KEYWORDS", ctx.Cfg.Gentoo.Arch)
		if err := ctx.appendFile("/etc/portage/make.conf",
			fmt.Sprintf("ACCEPT_KEYWORDS=\"~%s\"", ctx.Cfg.Gentoo.Arch)); err != nil {
			return fmt.Errorf("could not modify /etc/portage/make.conf: %w", err)
		}
	}

	ctx.Runner.log("Gentoo installation complete.")
	if ctx.Layout.Flags.UsedLuks {
		ctx.Runner.logf("A backup of your luks headers can be found at '%s', in case you want to have a backup.",
			LuksHeaderBackupDir)
	}
	ctx.Runner.logf("You may now reboot your system or execute 'gentooinstall chroot %s' to enter your system in a chroot.",
		RootMountpoint)
	return nil
}
