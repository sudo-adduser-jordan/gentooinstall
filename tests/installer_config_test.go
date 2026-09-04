package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gentooinstall/internal/installer"
)

func TestConfigureBaseSystemSystemd(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.System.Timezone = "Europe/Berlin"
	cfg.System.Keymap = "de"
	c, s := testContext(t, cfg, nil)
	mkScratchDir(t, c, "/etc")

	if err := installer.ConfigureBaseSystem(c); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, s,
		"locale-gen",
		"systemd-machine-id-setup",
		"ln -sfn ../usr/share/zoneinfo/Europe/Berlin /etc/localtime",
		"env-update",
	)
	if got := readScratch(t, c, "/etc/hostname"); got != "gentoo\n" {
		t.Fatalf("/etc/hostname = %q", got)
	}
	if got := readScratch(t, c, "/etc/vconsole.conf"); got != "KEYMAP=de\n" {
		t.Fatalf("/etc/vconsole.conf = %q", got)
	}
	if got := readScratch(t, c, "/etc/locale.conf"); got != "LANG=C.UTF-8\n" {
		t.Fatalf("/etc/locale.conf = %q", got)
	}
	if got := readScratch(t, c, "/etc/locale.gen"); got != "C.UTF-8 UTF-8\n" {
		t.Fatalf("/etc/locale.gen = %q", got)
	}
}

func TestConfigureBaseSystemOpenRC(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Stage3Variant = "openrc"
	cfg.System.Timezone = "UTC"
	cfg.System.Keymap = "de"
	c, s := testContext(t, cfg, nil)
	writeScratch(t, c, "/etc/conf.d/hostname", "hostname=\"host\"\n")
	writeScratch(t, c, "/etc/conf.d/keymaps", "keymap=\"us\"\n")

	if err := installer.ConfigureBaseSystem(c); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, s,
		"locale-gen",
		"emerge -v --config sys-libs/timezone-data",
		"eselect locale set C.UTF-8",
		"env-update",
	)
	if got := readScratch(t, c, "/etc/conf.d/hostname"); got != "hostname=\"gentoo\"\n" {
		t.Fatalf("/etc/conf.d/hostname = %q", got)
	}
	if got := readScratch(t, c, "/etc/conf.d/keymaps"); got != "keymap=\"de\"\n" {
		t.Fatalf("/etc/conf.d/keymaps = %q", got)
	}
	if got := readScratch(t, c, "/etc/timezone"); got != "UTC\n" {
		t.Fatalf("/etc/timezone = %q", got)
	}
}

func TestConfigureBaseSystemMuslOpenRC(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Stage3Variant = "musl"
	cfg.System.Timezone = "Asia/Tokyo"
	cfg.System.Keymap = "jp106"
	c, s := testContext(t, cfg, nil)
	writeScratch(t, c, "/etc/conf.d/hostname", "hostname=\"host\"\n")
	writeScratch(t, c, "/etc/conf.d/keymaps", "keymap=\"us\"\n")
	mkScratchDir(t, c, "/etc/env.d")

	if err := installer.ConfigureBaseSystem(c); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, s,
		"emerge --verbose sys-apps/musl-locales",
		"emerge -v sys-libs/timezone-data",
		"eselect locale set C.UTF-8",
		"env-update",
	)
	envDir := readScratch(t, c, "/etc/env.d/00local")
	for _, want := range []string{"MUSL_LOCPATH=\"/usr/share/i18n/locales/musl\"", "TZ=\"Asia/Tokyo\""} {
		if !strings.Contains(envDir, want) {
			t.Fatalf("/etc/env.d/00local missing %q:\n%s", want, envDir)
		}
	}
}

func TestConfigurePortage(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Packages.UseFlags = []string{"networkmanager", ""}
	c, _ := testContext(t, cfg, nil)

	if err := installer.ConfigurePortage(c); err != nil {
		t.Fatal(err)
	}
	makeConf := readScratch(t, c, "/etc/portage/make.conf")
	if !strings.Contains(makeConf, "MAKEOPTS=\"-j8\"") {
		t.Fatalf("make.conf missing MAKEOPTS:\n%s", makeConf)
	}
	if got := readScratch(t, c, "/etc/portage/package.use/user"); got != "networkmanager\n" {
		t.Fatalf("package.use/user = %q", got)
	}
	for _, f := range []string{"zz-autounmask", "zz-autounmask"} {
		if _, err := os.Stat(filepath.Join(c.Root, "/etc/portage/package.use/"+f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	if got := readScratch(t, c, "/etc/portage/package.keywords/zz-autounmask"); got != "" {
		t.Fatalf("package.keywords/zz-autounmask = %q", got)
	}
	if got := readScratch(t, c, "/etc/portage/package.license"); got != "" {
		t.Fatalf("package.license = %q", got)
	}
}

func TestConfigurePortageBinpkg(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Packages.EnableBinpkg = true
	c, s := testContext(t, cfg, nil)

	if err := installer.ConfigurePortage(c); err != nil {
		t.Fatal(err)
	}
	assertCmdContains(t, s, []string{"getuto", "chmod 644 /etc/portage/gnupg/pubring.kbx"})
	makeConf := readScratch(t, c, "/etc/portage/make.conf")
	if !strings.Contains(makeConf, "FEATURES=\"getbinpkg binpkg-request-signature\"") {
		t.Fatalf("make.conf missing binpkg FEATURES:\n%s", makeConf)
	}
}

func TestConfigurePortageMirrorselect(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.SelectMirrors = true
	cfg.Gentoo.SelectMirrorsLargeFile = true
	c, s := testContext(t, cfg, nil)

	if err := installer.ConfigurePortage(c); err != nil {
		t.Fatal(err)
	}
	assertCmdContains(t, s, []string{
		"emerge --verbose --oneshot app-portage/mirrorselect",
		"mirrorselect -s 4 -b 10 -D",
	})
}

func TestConfigureGitSync(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, s := testContext(t, cfg, nil)

	if err := installer.ConfigureGitSync(c); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, s, "emerge --sync")
	conf := readScratch(t, c, "/etc/portage/repos.conf/gentoo.conf")
	for _, want := range []string{"sync-type = git", "sync-depth = 1", "auto-sync = yes",
		"sync-git-verify-commit-signature = yes"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("gentoo.conf missing %q:\n%s", want, conf)
		}
	}
}

func TestConfigureGitSyncFullHistorySkipsSyncCommand(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.PortageGitFullHistory = true
	c, s := testContext(t, cfg, nil)

	if err := installer.ConfigureGitSync(c); err != nil {
		t.Fatal(err)
	}
	if got := readScratch(t, c, "/etc/portage/repos.conf/gentoo.conf"); !strings.Contains(got, "sync-depth = 0") {
		t.Fatalf("expected full history sync-depth = 0:\n%s", got)
	}
	assertCmds(t, s, "emerge --sync")
}

func TestConfigureGitSyncSkipsRsync(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.PortageSyncType = "rsync"
	c, s := testContext(t, cfg, nil)

	if err := installer.ConfigureGitSync(c); err != nil {
		t.Fatal(err)
	}
	if len(s.Calls()) != 0 {
		t.Fatalf("rsync sync should issue no commands, got %v", s.Lines())
	}
}

func TestEnableRepositories(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Packages.EnablingRepos = []string{"guru", "kde"}
	c, s := testContext(t, cfg, nil)

	if err := installer.EnableRepositories(c); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, s,
		"emerge --quiet app-eselect/eselect-repository",
		"eselect repository enable guru kde",
		"emaint sync -r guru",
		"emaint sync -r kde",
	)
}

func TestEnableRepositoriesNone(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, s := testContext(t, cfg, nil)

	if err := installer.EnableRepositories(c); err != nil {
		t.Fatal(err)
	}
	if len(s.Calls()) != 0 {
		t.Fatalf("no repos should issue no commands, got %v", s.Lines())
	}
}

func TestConfigureNetworkingSystemdDHCP(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, s := testContext(t, cfg, nil)

	if err := installer.ConfigureNetworking(c); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, s,
		"systemctl enable systemd-networkd",
		"systemctl enable systemd-resolved",
		"chown root:systemd-network /etc/systemd/network/20-wired.network",
		"chmod 640 /etc/systemd/network/20-wired.network",
	)
	unit := readScratch(t, c, "/etc/systemd/network/20-wired.network")
	if want := "[Match]\nName=en*\n\n[Network]\nDHCP=yes"; unit != want {
		t.Fatalf("networkd unit:\n%s\nwant:%s", unit, want)
	}
}

func TestConfigureNetworkingSystemdStatic(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.System.SystemdNetworkdDHCP = false
	c, _ := testContext(t, cfg, nil)

	if err := installer.ConfigureNetworking(c); err != nil {
		t.Fatal(err)
	}
	unit := readScratch(t, c, "/etc/systemd/network/20-wired.network")
	for _, want := range []string{"Address=192.168.1.100/32", "Address=fd00::1/64", "Gateway=192.168.1.1"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("networkd unit missing %q:\n%s", want, unit)
		}
	}
}

func TestConfigureNetworkingOpenRC(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Gentoo.Stage3Variant = "openrc"
	c, s := testContext(t, cfg, nil)

	if err := installer.ConfigureNetworking(c); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, s,
		"emerge --verbose net-misc/dhcpcd",
		"rc-update add dhcpcd default",
	)
}

func TestEnableSSHDAndAuthorizedKeys(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	cfg.Packages.RootSSHAuthorizedKeys = []string{"ssh-ed25519 AAAA test@host"}
	c, _ := testContext(t, cfg, nil)

	if err := installer.EnableSSHD(c); err != nil {
		t.Fatal(err)
	}
	if err := installer.InstallAuthorizedKeys(c); err != nil {
		t.Fatal(err)
	}
	if got := readScratch(t, c, "/etc/ssh/sshd_config"); !strings.HasPrefix(got, "#") {
		t.Fatalf("sshd_config = %q", got)
	}
	if got := readScratch(t, c, "/root/.ssh/authorized_keys"); got != "ssh-ed25519 AAAA test@host\n" {
		t.Fatalf("authorized_keys = %q", got)
	}
}

func TestEnableAuthorizedKeysNone(t *testing.T) {
	cfg := classicCfg("/dev/sdX", false, false)
	c, _ := testContext(t, cfg, nil)

	if err := installer.InstallAuthorizedKeys(c); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.Root, "/root/.ssh/authorized_keys")); err == nil {
		t.Fatal("authorized_keys written despite empty config")
	}
}

func TestEnableServiceSwitchByInit(t *testing.T) {
	systemd := classicCfg("/dev/sdX", false, false)
	cs, ss := testContext(t, systemd, nil)
	if err := installer.EnableService(cs, "foo"); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, ss, "systemctl enable foo")

	openrc := classicCfg("/dev/sdX", false, false)
	openrc.Gentoo.Stage3Variant = "openrc"
	co, so := testContext(t, openrc, nil)
	if err := installer.EnableService(co, "foo"); err != nil {
		t.Fatal(err)
	}
	assertCmds(t, so, "rc-update add foo default")
}
