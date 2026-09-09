// In-chroot system configurators (portage, repos, networking, sshd).
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gentooinstall/lib/installer"
)

func TestConfigureBaseSystemSystemd(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.System.Timezone = "Europe/Berlin"
	cfg.System.Keymap = "de"
	ctx, stub := testContext(testingT, cfg, nil)
	mkScratchDir(testingT, ctx, "/etc")

	if err := installer.ConfigureBaseSystem(ctx); err != nil {
		testingT.Fatal(err)
	}
	assertCmds(testingT, stub,
		"locale-gen",
		"systemd-machine-id-setup",
		"ln -sfn ../usr/share/zoneinfo/Europe/Berlin /etc/localtime",
		"env-update",
	)
	if got := readScratch(testingT, ctx, "/etc/hostname"); got != "gentoo\n" {
		testingT.Fatalf("/etc/hostname = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/vconsole.conf"); got != "KEYMAP=de\n" {
		testingT.Fatalf("/etc/vconsole.conf = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/locale.conf"); got != "LANG=C.UTF-8\n" {
		testingT.Fatalf("/etc/locale.conf = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/locale.gen"); got != "C.UTF-8 UTF-8\n" {
		testingT.Fatalf("/etc/locale.gen = %q", got)
	}
}

func TestConfigureBaseSystemOpenRC(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Stage3Variant = "openrc"
	cfg.System.Timezone = "UTC"
	cfg.System.Keymap = "de"
	ctx, stub := testContext(testingT, cfg, nil)
	writeScratch(testingT, ctx, "/etc/conf.d/hostname", "hostname=\"host\"\n")
	writeScratch(testingT, ctx, "/etc/conf.d/keymaps", "keymap=\"us\"\n")

	if err := installer.ConfigureBaseSystem(ctx); err != nil {
		testingT.Fatal(err)
	}
	assertCmds(testingT, stub,
		"locale-gen",
		"emerge -v --config sys-libs/timezone-data",
		"eselect locale set C.UTF-8",
		"env-update",
	)
	if got := readScratch(testingT, ctx, "/etc/conf.d/hostname"); got != "hostname=\"gentoo\"\n" {
		testingT.Fatalf("/etc/conf.d/hostname = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/conf.d/keymaps"); got != "keymap=\"de\"\n" {
		testingT.Fatalf("/etc/conf.d/keymaps = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/timezone"); got != "UTC\n" {
		testingT.Fatalf("/etc/timezone = %q", got)
	}
}

func TestConfigureBaseSystemMuslOpenRC(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Stage3Variant = "musl"
	cfg.System.Timezone = "Asia/Tokyo"
	cfg.System.Keymap = "jp106"
	ctx, stub := testContext(testingT, cfg, nil)
	writeScratch(testingT, ctx, "/etc/conf.d/hostname", "hostname=\"host\"\n")
	writeScratch(testingT, ctx, "/etc/conf.d/keymaps", "keymap=\"us\"\n")
	mkScratchDir(testingT, ctx, "/etc/env.d")

	if err := installer.ConfigureBaseSystem(ctx); err != nil {
		testingT.Fatal(err)
	}
	assertCmds(testingT, stub,
		"emerge --verbose sys-apps/musl-locales",
		"emerge -v sys-libs/timezone-data",
		"eselect locale set C.UTF-8",
		"env-update",
	)
	envDir := readScratch(testingT, ctx, "/etc/env.d/00local")
	for _, want := range []string{"MUSL_LOCPATH=\"/usr/share/i18n/locales/musl\"", "TZ=\"Asia/Tokyo\""} {
		if !strings.Contains(envDir, want) {
			testingT.Fatalf("/etc/env.d/00local missing %q:\n%s", want, envDir)
		}
	}
}

func TestConfigurePortage(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Packages.UseFlags = []string{"networkmanager", ""}
	ctx, _ := testContext(testingT, cfg, nil)

	if err := installer.ConfigurePortage(ctx); err != nil {
		testingT.Fatal(err)
	}
	makeConf := readScratch(testingT, ctx, "/etc/portage/make.conf")
	if !strings.Contains(makeConf, "MAKEOPTS=\"-j8\"") {
		testingT.Fatalf("make.conf missing MAKEOPTS:\n%s", makeConf)
	}
	if got := readScratch(testingT, ctx, "/etc/portage/package.use/user"); got != "networkmanager\n" {
		testingT.Fatalf("package.use/user = %q", got)
	}
	for _, name := range []string{"zz-autounmask", "zz-autounmask"} {
		if _, err := os.Stat(filepath.Join(ctx.Root, "/etc/portage/package.use/"+name)); err != nil {
			testingT.Fatalf("missing %s: %v", name, err)
		}
	}
	if got := readScratch(testingT, ctx, "/etc/portage/package.keywords/zz-autounmask"); got != "" {
		testingT.Fatalf("package.keywords/zz-autounmask = %q", got)
	}
	if got := readScratch(testingT, ctx, "/etc/portage/package.license"); got != "" {
		testingT.Fatalf("package.license = %q", got)
	}
}

func TestConfigurePortageBinpkg(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Packages.EnableBinpkg = true
	ctx, stub := testContext(testingT, cfg, nil)

	if err := installer.ConfigurePortage(ctx); err != nil {
		testingT.Fatal(err)
	}
	assertCmdContains(testingT, stub, []string{"getuto", "chmod 644 /etc/portage/gnupg/pubring.kbx"})
	makeConf := readScratch(testingT, ctx, "/etc/portage/make.conf")
	if !strings.Contains(makeConf, "FEATURES=\"getbinpkg binpkg-request-signature\"") {
		testingT.Fatalf("make.conf missing binpkg FEATURES:\n%s", makeConf)
	}
	if !strings.Contains(makeConf, "FEATURES=\"parallel-fetch\"") {
		testingT.Fatalf("make.conf missing parallel-fetch:\n%s", makeConf)
	}
}

func TestConfigurePortageMirrorselect(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.SelectMirrors = true
	cfg.Gentoo.SelectMirrorsLargeFile = true
	ctx, stub := testContext(testingT, cfg, nil)

	if err := installer.ConfigurePortage(ctx); err != nil {
		testingT.Fatal(err)
	}
	assertCmdContains(testingT, stub, []string{
		"emerge --verbose --oneshot app-portage/mirrorselect",
		"mirrorselect -s 4 -b 10 -D",
	})
}

func TestConfigureGitSync(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, nil)

	if err := installer.ConfigureGitSync(ctx); err != nil {
		testingT.Fatal(err)
	}
	assertCmds(testingT, stub, "emerge --sync")
	conf := readScratch(testingT, ctx, "/etc/portage/repos.conf/gentoo.conf")
	for _, want := range []string{"sync-type = git", "sync-depth = 1", "auto-sync = yes",
		"sync-git-verify-commit-signature = yes"} {
		if !strings.Contains(conf, want) {
			testingT.Fatalf("gentoo.conf missing %q:\n%s", want, conf)
		}
	}
}

func TestConfigureGitSyncFullHistorySkipsSyncCommand(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.PortageGitFullHistory = true
	ctx, stub := testContext(testingT, cfg, nil)

	if err := installer.ConfigureGitSync(ctx); err != nil {
		testingT.Fatal(err)
	}
	if got := readScratch(testingT, ctx, "/etc/portage/repos.conf/gentoo.conf"); !strings.Contains(got, "sync-depth = 0") {
		testingT.Fatalf("expected full history sync-depth = 0:\n%s", got)
	}
	assertCmds(testingT, stub, "emerge --sync")
}

func TestConfigureGitSyncSkipsRsync(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.PortageSyncType = "rsync"
	ctx, stub := testContext(testingT, cfg, nil)

	if err := installer.ConfigureGitSync(ctx); err != nil {
		testingT.Fatal(err)
	}
	if len(stub.Calls()) != 0 {
		testingT.Fatalf("rsync sync should issue no commands, got %v", stub.Lines())
	}
}

func TestWriteReposConfGit(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, nil)

	if err := installer.WriteReposConf(ctx); err != nil {
		testingT.Fatal(err)
	}
	conf := readScratch(testingT, ctx, "/etc/portage/repos.conf/gentoo.conf")
	for _, want := range []string{"main-repo = gentoo", "sync-type = git",
		"sync-depth = 1", "sync-uri = https://anongit.gentoo.org/git/repo/sync/gentoo.git",
		"auto-sync = yes", "sync-git-verify-commit-signature = yes"} {
		if !strings.Contains(conf, want) {
			testingT.Fatalf("gentoo.conf missing %q:\n%s", want, conf)
		}
	}
	if _, err := os.Stat(filepath.Join(ctx.Root, "/var/db/repos/gentoo")); err != nil {
		testingT.Fatalf("repository location should exist: %v", err)
	}
	if len(stub.Calls()) != 0 {
		testingT.Fatalf("WriteReposConf should issue no commands, got %v", stub.Lines())
	}
}

func TestWriteReposConfRsync(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.PortageSyncType = "rsync"
	cfg.Gentoo.PortageRsyncMirror = "rsync://mirror.example.invalid/gentoo-portage"
	ctx, stub := testContext(testingT, cfg, nil)

	if err := installer.WriteReposConf(ctx); err != nil {
		testingT.Fatal(err)
	}
	conf := readScratch(testingT, ctx, "/etc/portage/repos.conf/gentoo.conf")
	for _, want := range []string{"main-repo = gentoo", "sync-type = rsync",
		"sync-uri = rsync://mirror.example.invalid/gentoo-portage", "auto-sync = yes"} {
		if !strings.Contains(conf, want) {
			testingT.Fatalf("gentoo.conf missing %q:\n%s", want, conf)
		}
	}
	if _, err := os.Stat(filepath.Join(ctx.Root, "/var/db/repos/gentoo")); err != nil {
		testingT.Fatalf("repository location should exist: %v", err)
	}
	if len(stub.Calls()) != 0 {
		testingT.Fatalf("WriteReposConf should issue no commands, got %v", stub.Lines())
	}
}

// The seed phase must register an rsync-typed repo even when git is
// configured: emerge-webrsync rejects git-typed repos with an "invalid sync
// type" validation failure. The flip to git happens later in
// ConfigureGitSync.
func TestSeedPortageTree(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	if cfg.Gentoo.PortageSyncType != "git" {
		testingT.Fatalf("precondition: default sync type = %q, want git", cfg.Gentoo.PortageSyncType)
	}
	ctx, stub := testContext(testingT, cfg, nil)
	// A stale git checkout from a previous run must not survive the seed.
	mkScratchDir(testingT, ctx, "/var/db/repos/gentoo/.git")

	if err := installer.SeedPortageTree(ctx); err != nil {
		testingT.Fatal(err)
	}
	conf := readScratch(testingT, ctx, "/etc/portage/repos.conf/gentoo.conf")
	for _, want := range []string{"sync-type = rsync", "auto-sync = yes"} {
		if !strings.Contains(conf, want) {
			testingT.Fatalf("seed gentoo.conf missing %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "sync-type = git") {
		testingT.Fatalf("seed gentoo.conf must not be git-typed:\n%s", conf)
	}
	if _, err := os.Stat(filepath.Join(ctx.Root, "/var/db/repos/gentoo/.git")); !os.IsNotExist(err) {
		testingT.Fatalf("stale .git checkout should be cleared, stat err: %v", err)
	}
	assertCmds(testingT, stub, "emerge-webrsync")
}

func TestEnableRepositories(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Packages.EnablingRepos = []string{"guru", "kde"}
	ctx, stub := testContext(testingT, cfg, nil)

	if err := installer.EnableRepositories(ctx); err != nil {
		testingT.Fatal(err)
	}
	assertCmds(testingT, stub,
		"emerge --quiet app-eselect/eselect-repository",
		"eselect repository enable guru kde",
		"emaint sync -r guru",
		"emaint sync -r kde",
	)
}

func TestEnableRepositoriesNone(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, nil)

	if err := installer.EnableRepositories(ctx); err != nil {
		testingT.Fatal(err)
	}
	if len(stub.Calls()) != 0 {
		testingT.Fatalf("no repos should issue no commands, got %v", stub.Lines())
	}
}

func TestConfigureNetworkingSystemdDHCP(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, stub := testContext(testingT, cfg, nil)

	if err := installer.ConfigureNetworking(ctx); err != nil {
		testingT.Fatal(err)
	}
	assertCmds(testingT, stub,
		"systemctl enable systemd-networkd",
		"systemctl enable systemd-resolved",
		"chown root:systemd-network /etc/systemd/network/20-wired.network",
		"chmod 640 /etc/systemd/network/20-wired.network",
	)
	unit := readScratch(testingT, ctx, "/etc/systemd/network/20-wired.network")
	if want := "[Match]\nName=en*\n\n[Network]\nDHCP=yes"; unit != want {
		testingT.Fatalf("networkd unit:\n%s\nwant:%s", unit, want)
	}
}

func TestConfigureNetworkingSystemdStatic(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.System.SystemdNetworkdDHCP = false
	ctx, _ := testContext(testingT, cfg, nil)

	if err := installer.ConfigureNetworking(ctx); err != nil {
		testingT.Fatal(err)
	}
	unit := readScratch(testingT, ctx, "/etc/systemd/network/20-wired.network")
	for _, want := range []string{"Address=192.168.1.100/32", "Address=fd00::1/64", "Gateway=192.168.1.1"} {
		if !strings.Contains(unit, want) {
			testingT.Fatalf("networkd unit missing %q:\n%s", want, unit)
		}
	}
}

func TestConfigureNetworkingOpenRC(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Stage3Variant = "openrc"
	ctx, stub := testContext(testingT, cfg, nil)

	if err := installer.ConfigureNetworking(ctx); err != nil {
		testingT.Fatal(err)
	}
	assertCmds(testingT, stub,
		"emerge --verbose net-misc/dhcpcd",
		"rc-update add dhcpcd default",
	)
}

func TestEnableSSHDAndAuthorizedKeys(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Packages.RootSSHAuthorizedKeys = []string{"ssh-ed25519 AAAA test@host"}
	ctx, _ := testContext(testingT, cfg, nil)

	if err := installer.EnableSSHD(ctx); err != nil {
		testingT.Fatal(err)
	}
	if err := installer.InstallAuthorizedKeys(ctx); err != nil {
		testingT.Fatal(err)
	}
	if got := readScratch(testingT, ctx, "/etc/ssh/sshd_config"); !strings.HasPrefix(got, "#") {
		testingT.Fatalf("sshd_config = %q", got)
	}
	if got := readScratch(testingT, ctx, "/root/.ssh/authorized_keys"); got != "ssh-ed25519 AAAA test@host\n" {
		testingT.Fatalf("authorized_keys = %q", got)
	}
}

func TestEnableAuthorizedKeysNone(testingT *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	ctx, _ := testContext(testingT, cfg, nil)

	if err := installer.InstallAuthorizedKeys(ctx); err != nil {
		testingT.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ctx.Root, "/root/.ssh/authorized_keys")); err == nil {
		testingT.Fatal("authorized_keys written despite empty config")
	}
}

func TestEnableServiceSwitchByInit(testingT *testing.T) {
	systemd := classicCfg("/dev/sdX", false, false)
	cs, ss := testContext(testingT, systemd, nil)
	if err := installer.EnableService(cs, "foo"); err != nil {
		testingT.Fatal(err)
	}
	assertCmds(testingT, ss, "systemctl enable foo")

	openrc := classicCfg("/dev/sdX", false, false)
	openrc.Gentoo.Stage3Variant = "openrc"
	co, so := testContext(testingT, openrc, nil)
	if err := installer.EnableService(co, "foo"); err != nil {
		testingT.Fatal(err)
	}
	assertCmds(testingT, so, "rc-update add foo default")
}
