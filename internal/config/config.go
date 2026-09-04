// Package config defines the gentooinstall configuration model and its TOML
// representation (gentoo.toml).
package config

import (
	"fmt"
	"regexp"
	"strings"

	"gentooinstall/internal/pkglists"
)

// Scheme names for [disk].
const (
	SchemeClassic    = "classic_single_disk"
	SchemeExisting   = "existing_partitions"
	SchemeZFSCentric = "zfs_centric"
	SchemeBtrfs      = "btrfs_centric"
	SchemeRaid0Luks  = "raid0_luks"
	SchemeRaid1Luks  = "raid1_luks"
	SchemeCustom     = "custom"
)

// Schemes lists all supported partitioning schemes with a short description.
var Schemes = []struct {
	Name string
	Desc string
}{
	{SchemeClassic, "Classic single disk layout (boot/efi, swap?, root)"},
	{SchemeExisting, "Skip partitioning, use existing pre-formatted partitions"},
	{SchemeZFSCentric, "ZFS centric (optional ZFS compression and encryption)"},
	{SchemeBtrfs, "Btrfs centric (optional raid0/1 via btrfs)"},
	{SchemeRaid0Luks, "Raid0 (N>=2 disks) and luks for root"},
	{SchemeRaid1Luks, "Raid1 (N>=2 disks) and luks for root"},
	{SchemeCustom, "Custom (expert option; declarative action list in [disk.custom])"},
}

// Stage3Variant is one selectable stage3 tarball variant.
type Stage3Variant struct {
	ID          string
	Description string
}

// Stage3Variants mirrors ALL_STAGE3_VARIANTS from the bash configurator.
var Stage3Variants = []Stage3Variant{
	{"openrc", "Minimal OpenRC base (recommended)"},
	{"openrc-splitusr", "Minimal OpenRC base with a split-usr filesystem layout"},
	{"desktop-openrc", "OpenRC, desktop profile, might have blockers"},
	{"systemd", "Minimal systemd base (recommended)"},
	{"desktop-systemd", "systemd, desktop profile, might have blockers"},
	{"nomultilib-openrc", "Minimal OpenRC base without 32bits support (Experimental)"},
	{"nomultilib-systemd", "Minimal systemd base without 32bits support (Experimental)"},
	{"x32-openrc", "Minimal OpenRC base without 64bits support (Experimental)"},
	{"x32-systemd", "Minimal systemd base without 64bits support (Experimental)"},
	{"llvm-openrc", "Minimal OpenRC base compiled with LLVM (Experimental)"},
	{"llvm-systemd", "Minimal systemd base compiled with LLVM (Experimental)"},
	{"hardened-openrc", "Hardened OpenRC base (Experimental)"},
	{"hardened-selinux-openrc", "Hardened OpenRC base with SELinux (Experimental)"},
	{"musl", "Minimal OpenRC base using musl (Experimental)"},
	{"musl-llvm", "Minimal OpenRC base using musl compiled with LLVM (Experimental)"},
	{"musl-hardened", "Hardened OpenRC base using musl (Experimental)"},
}

// Archs lists supported gentoo architectures.
var Archs = []string{"x86", "amd64", "arm", "arm64"}

// SubArchs lists supported x86 sub-architectures.
var SubArchs = []string{"i486", "i686"}

// Disk holds the [disk] section.
type Disk struct {
	Scheme     string   `toml:"scheme"`
	BootType   string   `toml:"boot_type"` // efi | bios
	Device     string   `toml:"device"`
	Devices    []string `toml:"devices"`
	BootDevice string   `toml:"boot_device"`

	UseSwap  bool   `toml:"use_swap"`
	SwapSize string `toml:"swap_size"`

	SwapDevice string `toml:"swap_device"` // existing_partitions only

	UseLuks bool   `toml:"use_luks"`
	RootFS  string `toml:"root_fs"` // ext4 | btrfs

	ZFSPoolType    string `toml:"zfs_pool_type"` // standard | custom
	ZFSEncrypt     bool   `toml:"zfs_encrypt"`
	ZFSUseCompress bool   `toml:"zfs_use_compression"`
	ZFSCompression string `toml:"zfs_compression"`
	BtrfsRaidType  string `toml:"btrfs_raid_type"` // raid0 | raid1

	Custom []CustomAction `toml:"custom"`
}

// CustomAction is one declarative disk action used by the custom scheme.
type CustomAction struct {
	Action   string   `toml:"action"`
	NewID    string   `toml:"new_id,omitempty"`
	ID       string   `toml:"id,omitempty"`
	Device   string   `toml:"device,omitempty"`
	Size     string   `toml:"size,omitempty"`
	Type     string   `toml:"type,omitempty"`
	Label    string   `toml:"label,omitempty"`
	Level    string   `toml:"level,omitempty"`
	Name     string   `toml:"name,omitempty"`
	IDs      []string `toml:"ids,omitempty"`
	PoolType string   `toml:"pool_type,omitempty"`
	Encrypt  bool     `toml:"encrypt,omitempty"`
	Compress string   `toml:"compress,omitempty"` // "" disables compression
	RaidType string   `toml:"raid_type,omitempty"`
}

// System holds the [system] section.
type System struct {
	Hostname             string   `toml:"hostname"`
	Timezone             string   `toml:"timezone"`
	Keymap               string   `toml:"keymap"`
	KeymapInitramfsOther bool     `toml:"keymap_initramfs_other"`
	KeymapInitramfs      string   `toml:"keymap_initramfs"`
	Locales              []string `toml:"locales"` // locale.gen lines, e.g. "en_US.UTF-8 UTF-8"
	Locale               string   `toml:"locale"`

	Systemd                      bool     `toml:"-"`
	SystemdNetworkd              bool     `toml:"systemd_networkd"`
	SystemdNetworkdInterfaceName string   `toml:"systemd_networkd_interface_name"`
	SystemdNetworkdDHCP          bool     `toml:"systemd_networkd_dhcp"`
	SystemdNetworkdAddresses     []string `toml:"systemd_networkd_addresses"`
	SystemdNetworkdGateway       string   `toml:"systemd_networkd_gateway"`
	InitramfsSSHD                bool     `toml:"initramfs_sshd"`
}

// Profile identifies one selectable Gentoo profile (eselect profile).
type Profile struct {
	ID   string
	Desc string
	// Packages are the curated portage atoms typically installed for this
	// profile. Gentoo profiles themselves do not ship a fixed package set
	// (the @profile set is empty); these are maintained here as a sensible
	// default set (often the environment's meta-packages) used both for
	// display and, when enabled, for installation.
	Packages []string
}

// Profiles lists the selectable eselect profiles. It covers the common
// amd64 23.0 profiles and is intentionally the single place to extend.
var Profiles = []Profile{
	{
		ID: "default/linux/amd64/23.0", Desc: "Minimal base (OpenRC)",
		Packages: nil,
	},
	{
		ID: "default/linux/amd64/23.0/systemd", Desc: "Minimal base (systemd)",
		Packages: nil,
	},
	{
		ID: "default/linux/amd64/23.0/desktop", Desc: "Desktop (OpenRC)",
		Packages: []string{
			"x11-base/xorg-server", "x11-apps/xrandr", "x11-apps/xsetroot",
			"x11-misc/colord", "app-admin/desktop-file-utils", "sys-apps/dbus",
			"app-accessibility/at-spi2-core",
		},
	},
	{
		ID: "default/linux/amd64/23.0/desktop/systemd", Desc: "Desktop (systemd)",
		Packages: []string{
			"x11-base/xorg-server", "x11-apps/xrandr", "x11-apps/xsetroot",
			"x11-misc/colord", "app-admin/desktop-file-utils", "sys-apps/dbus",
			"app-accessibility/at-spi2-core",
		},
	},
	{
		ID: "default/linux/amd64/23.0/desktop/gnome", Desc: "GNOME desktop (OpenRC)",
		Packages: []string{
			"x11-base/xorg-server", "x11-apps/xrandr", "x11-apps/xsetroot",
			"app-admin/desktop-file-utils", "sys-apps/dbus", "gnome-base/gnome",
		},
	},
	{
		ID: "default/linux/amd64/23.0/desktop/gnome/systemd", Desc: "GNOME desktop (systemd)",
		Packages: []string{
			"x11-base/xorg-server", "x11-apps/xrandr", "x11-apps/xsetroot",
			"app-admin/desktop-file-utils", "sys-apps/dbus", "gnome-base/gnome",
		},
	},
	{
		ID: "default/linux/amd64/23.0/desktop/kde", Desc: "KDE Plasma desktop (OpenRC)",
		Packages: []string{
			"x11-base/xorg-server", "x11-apps/xrandr", "x11-apps/xsetroot",
			"app-admin/desktop-file-utils", "sys-apps/dbus", "kde-plasma/plasma-meta",
		},
	},
	{
		ID: "default/linux/amd64/23.0/desktop/kde/systemd", Desc: "KDE Plasma desktop (systemd)",
		Packages: []string{
			"x11-base/xorg-server", "x11-apps/xrandr", "x11-apps/xsetroot",
			"app-admin/desktop-file-utils", "sys-apps/dbus", "kde-plasma/plasma-meta",
		},
	},
	{
		ID: "default/linux/amd64/23.0/no-multilib", Desc: "64-bit only, no 32-bit support (OpenRC)",
		Packages: nil,
	},
	{
		ID: "default/linux/amd64/23.0/no-multilib/systemd", Desc: "64-bit only, no 32-bit support (systemd)",
		Packages: nil,
	},
	{
		ID: "default/linux/amd64/23.0/musl", Desc: "Minimal musl (OpenRC)",
		Packages: nil,
	},
}

// Gentoo holds the [gentoo] section.
type Gentoo struct {
	Mirror                 string `toml:"mirror"`
	Arch                   string `toml:"arch"`
	Subarch                string `toml:"subarch"`
	Stage3Variant          string `toml:"stage3_variant"`
	Profile                string `toml:"profile"`
	PortageSyncType        string `toml:"portage_sync_type"` // git | rsync
	PortageGitFullHistory  bool   `toml:"portage_git_full_history"`
	PortageGitMirror       string `toml:"portage_git_mirror"`
	UsePortageTesting      bool   `toml:"use_portage_testing"`
	SelectMirrors          bool   `toml:"select_mirrors"`
	SelectMirrorsLargeFile bool   `toml:"select_mirrors_large_file"`
}

// Packages holds the [packages] section.
type Packages struct {
	Additional            []string `toml:"additional"`
	CustomPackages        []string `toml:"custom_packages"`
	EnablingRepos         []string `toml:"enabling_repos"`
	EnableSSHD            bool     `toml:"enable_sshd"`
	EnableBinpkg          bool     `toml:"enable_binpkg"`
	KernelType            string   `toml:"kernel_type"` // bin | source
	KernelDeblob          bool     `toml:"kernel_deblob"`
	RootSSHAuthorizedKeys []string `toml:"ssh_authorized_keys"`
	// UseFlags are /etc/portage/package.use lines, e.g. "dev-libs/openssl -asm".
	UseFlags []string `toml:"use_flags"`
}

// MakeConf holds the [makeconf] section: user-selected make.conf options to
// append to /etc/portage/make.conf at the end of installation, plus a
// freeform block of extra content.
type MakeConf struct {
	// Options are the keys of MakeConfOptions that were toggled on.
	Options []string `toml:"options"`
	// Extra is arbitrary multi-line content appended verbatim.
	Extra string `toml:"extra"`
}

// MakeConfOption is one selectable make.conf option.
type MakeConfOption struct {
	Key  string
	Desc string
	// Line is the make.conf line appended when the option is picked.
	Line string
}

// MakeConfOptions is the fixed catalog of make.conf options offered in the
// TUI picker.
var MakeConfOptions = []MakeConfOption{
	{
		Key: "jobs", Desc: "Parallel build jobs (-jN) matching the CPU count",
		Line: `MAKEOPTS="-j${JOBS}"`,
	},
	{
		Key: "use_testing", Desc: `Accept ~arch keywords (ACCEPT_KEYWORDS)`,
		Line: `ACCEPT_KEYWORDS="~${ARCH}"`,
	},
	{
		Key: "binpkg", Desc: "Use binary packages (FEATURES getbinpkg)",
		Line: `FEATURES="getbinpkg binpkg-request-signature"`,
	},
	{
		Key: "accept_license", Desc: "Accept all licenses (ACCEPT_LICENSE)",
		Line: `ACCEPT_LICENSE="*"`,
	},
	{
		Key: "distcc", Desc: "Enable distcc for parallel distributed builds",
		Line: `FEATURES="distcc"`,
	},
	{
		Key: "ccache", Desc: "Enable ccache for build caching",
		Line: `FEATURES="ccache"`,
	},
}

// LookupMakeConfOption returns the catalog entry for a key, or nil.
func LookupMakeConfOption(key string) *MakeConfOption {
	for i := range MakeConfOptions {
		if MakeConfOptions[i].Key == key {
			return &MakeConfOptions[i]
		}
	}
	return nil
}

// Config is the whole gentoo.toml file.
type Config struct {
	Disk     Disk     `toml:"disk"`
	System   System   `toml:"system"`
	Gentoo   Gentoo   `toml:"gentoo"`
	Packages Packages `toml:"packages"`
	MakeConf MakeConf `toml:"makeconf"`
}

// Default returns the built-in default configuration
// (port of load_default_config).
func Default(hasEFI bool) *Config {
	boot := "bios"
	if hasEFI {
		boot = "efi"
	}
	return &Config{
		Disk: Disk{
			Scheme:         SchemeClassic,
			BootType:       boot,
			Device:         "/dev/sdX",
			Devices:        nil,
			UseSwap:        true,
			SwapSize:       "8GiB",
			UseLuks:        true,
			RootFS:         "ext4",
			ZFSPoolType:    "standard",
			ZFSCompression: "zstd",
			BtrfsRaidType:  "raid0",
		},
		System: System{
			Hostname:             "gentoo",
			KeymapInitramfsOther: false,
			Locales:              []string{"C.UTF-8 UTF-8"},
			Locale:               "C.UTF-8",

			SystemdNetworkd:              true,
			SystemdNetworkdInterfaceName: "en*",
			SystemdNetworkdDHCP:          true,
			SystemdNetworkdAddresses:     []string{"192.168.1.100/32", "fd00::1/64"},
			SystemdNetworkdGateway:       "192.168.1.1",
			InitramfsSSHD:                false,
		},
		Gentoo: Gentoo{
			Mirror:            "https://mirror.leaseweb.com/gentoo",
			Arch:              "amd64",
			Stage3Variant:     "systemd",
			PortageSyncType:   "git",
			PortageGitMirror:  "https://anongit.gentoo.org/git/repo/sync/gentoo.git",
			UsePortageTesting: true,
		},
		Packages: Packages{
			EnableSSHD: true,
			KernelType: "bin",
		},
	}
}

// LookupProfile returns the profile with the given id, or nil.
func LookupProfile(id string) *Profile {
	for i := range Profiles {
		if Profiles[i].ID == id {
			return &Profiles[i]
		}
	}
	return nil
}

// Repo identifies a synced ebuild repository (overlay) that can be enabled
// via eselect repository. The "gentoo" repo is always present; the others
// are optional overlays the user can enable.
type Repo struct {
	Name string
	Desc string
	// IndexURL is the base URL of the repo's synced tree. Its complete
	// package list is its "metadata/pkg_desc_index" file, downloaded into
	// data/repos/<name>.packages by scripts/packages.sh and embedded
	// at build time (see internal/pkglists). It is not used at runtime.
	IndexURL string
}

// Overlays lists the optional third-party repositories offered in the TUI,
// in addition to the always-on "gentoo" repo.
var Overlays = []Repo{
	{Name: "guru", Desc: "Community-curated GURU overlay",
		IndexURL: "https://github.com/gentoo-mirror/guru"},
	{Name: "kde", Desc: "KDE official overlay",
		IndexURL: "https://github.com/gentoo-mirror/kde"},
	{Name: "cachyos", Desc: "CachyOS performance overlay",
		IndexURL: "https://github.com/gentoo-mirror/cachyos"},
	{Name: "librewolf", Desc: "LibreWolf browser overlay",
		IndexURL: "https://gitlab.com/librewolf-community/browser/gentoo"},
}

// MainRepoIndexURL is the base URL of the main Gentoo ebuild repository
// (rsync-friendly mirror) whose "metadata/pkg_desc_index" feeds
// data/repos/gentoo.packages.
const MainRepoIndexURL = "https://mirrors.kernel.org/gentoo-portage"

// LookupOverlay returns the overlay with the given name, or nil.
func LookupOverlay(name string) *Repo {
	for i := range Overlays {
		if Overlays[i].Name == name {
			return &Overlays[i]
		}
	}
	return nil
}

// RepoPackages returns the complete catalog of atoms to offer in the
// additional-packages picker. It unions the always-on gentoo repo with the
// packages of each enabled overlay, drawn from the statically embedded
// package lists. Repos with no static list present are skipped (their items
// can still be added via the "Custom atoms" field).
func RepoPackages(enabled []string) []string {
	seen := map[string]bool{}
	all := make([]string, 0, 256)
	add := func(atoms []string) {
		for _, a := range atoms {
			if seen[a] {
				continue
			}
			seen[a] = true
			all = append(all, a)
		}
	}
	add(pkglists.Atoms("gentoo"))
	for _, name := range enabled {
		if LookupOverlay(name) != nil {
			add(pkglists.Atoms(name))
		}
	}
	return all
}

// ProfileDesc returns the human-readable description of a profile, or ""
// for an unknown/empty id. It mirrors Desc so the TUI can show friendly
// names in place of the full profile path.
func ProfileDesc(id string) string {
	if p := LookupProfile(id); p != nil {
		return p.Desc
	}
	return ""
}

// ProfileUsesSystemd reports whether the profile id selects a systemd init.
// It matches the real Gentoo naming convention where systemd profiles end
// in a "/systemd" segment.
func ProfileUsesSystemd(id string) bool {
	return strings.HasSuffix(id, "/systemd")
}

// ProfilePackages returns the curated package set for the currently
// selected profile. It is empty when no (known) profile is selected.
func (c *Config) ProfilePackages() []string {
	p := LookupProfile(c.Gentoo.Profile)
	if p == nil {
		return nil
	}
	return p.Packages
}

// UsesSystemd reports whether the selected stage3 variant uses systemd.
func (c *Config) UsesSystemd() bool { return strings.Contains(c.Gentoo.Stage3Variant, "systemd") }

// UsesMusl reports whether the selected stage3 variant is musl-based.
func (c *Config) UsesMusl() bool { return strings.Contains(c.Gentoo.Stage3Variant, "musl") }

// Stage3BaseName returns "stage3-$arch-$variant".
func (c *Config) Stage3BaseName() string {
	return fmt.Sprintf("stage3-%s-%s", c.Gentoo.Arch, c.Gentoo.Stage3Variant)
}

// Stage3BaseNameCustom handles the x32 / x86-subarch naming special case.
func (c *Config) Stage3BaseNameCustom() string {
	if strings.Contains(c.Gentoo.Stage3Variant, "x32") {
		return "stage3-" + c.Gentoo.Stage3Variant
	}
	return fmt.Sprintf("stage3-%s-%s", c.Gentoo.Subarch, c.Gentoo.Stage3Variant)
}

// Stage3BaseNameFinal picks the basename actually used for downloads.
func (c *Config) Stage3BaseNameFinal() string {
	if (c.Gentoo.Arch == "amd64" && strings.Contains(c.Gentoo.Stage3Variant, "x32")) ||
		(c.Gentoo.Arch == "x86" && c.Gentoo.Subarch != "") {
		return c.Stage3BaseNameCustom()
	}
	return c.Stage3BaseName()
}

var hostnameRe = regexp.MustCompile(
	`^(([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]*[a-zA-Z0-9])\.)*([A-Za-z0-9]|[A-Za-z0-9][A-Za-z0-9\-]*[A-Za-z0-9])$`)

var keymapRe = regexp.MustCompile(`^[0-9A-Za-z-]*$`)

// Validate checks semantic correctness of the configuration
// (everything checkable without touching disks).
func (c *Config) Validate() []error {
	var errs []error
	addf := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if !keymapRe.MatchString(c.System.Keymap) {
		addf("KEYMAP %q contains invalid characters", c.System.Keymap)
	}
	if !hostnameRe.MatchString(c.System.Hostname) {
		addf("%q is not a valid hostname", c.System.Hostname)
	}

	switch c.System.Locale {
	case "":
		addf("no default locale set")
	}
	if len(c.System.Locales) == 0 {
		addf("no locales to generate")
	}

	validScheme := false
	for _, s := range Schemes {
		if s.Name == c.Disk.Scheme {
			validScheme = true
			break
		}
	}
	if !validScheme {
		addf("unknown partitioning scheme %q", c.Disk.Scheme)
		return errs
	}

	if c.Disk.Scheme != SchemeCustom {
		if c.Disk.BootType != "efi" && c.Disk.BootType != "bios" {
			addf("invalid boot type %q", c.Disk.BootType)
		}
		multi := map[string]bool{
			SchemeZFSCentric: true, SchemeBtrfs: true,
			SchemeRaid0Luks: true, SchemeRaid1Luks: true,
		}[c.Disk.Scheme]
		single := map[string]bool{SchemeClassic: true, SchemeExisting: true}[c.Disk.Scheme]

		if single && c.Disk.Device == "" {
			addf("no device configured for scheme %q", c.Disk.Scheme)
		}
		if multi && len(c.Disk.Devices) == 0 {
			addf("no devices configured for scheme %q", c.Disk.Scheme)
		}
		if multi && c.Disk.Scheme != SchemeZFSCentric && c.Disk.Scheme != SchemeBtrfs &&
			len(c.Disk.Devices) < 2 {
			addf("scheme %q needs at least 2 devices", c.Disk.Scheme)
		}
		if c.Disk.Scheme == SchemeExisting && c.Disk.BootDevice == "" {
			addf("existing_partitions needs boot_device")
		}
		if c.Disk.UseSwap && c.Disk.SwapSize == "" && c.Disk.Scheme != SchemeExisting {
			addf("swap enabled but no swap size given")
		}
	}

	switch c.Gentoo.Arch {
	case "x86":
	default:
		if c.Gentoo.Subarch != "" {
			addf("subarch only valid for x86")
		}
	}
	found := false
	for _, a := range Archs {
		if a == c.Gentoo.Arch {
			found = true
		}
	}
	if !found {
		addf("unknown architecture %q", c.Gentoo.Arch)
	}

	vfound := false
	for _, v := range Stage3Variants {
		if v.ID == c.Gentoo.Stage3Variant {
			vfound = true
		}
	}
	if !vfound {
		addf("unknown stage3 variant %q", c.Gentoo.Stage3Variant)
	} else if c.UsesSystemd() != c.System.SystemdNetworkd {
		// networkd options only apply to systemd; not fatal but suspicious.
	}

	if c.Gentoo.Profile != "" && LookupProfile(c.Gentoo.Profile) == nil {
		addf("unknown profile %q", c.Gentoo.Profile)
	} else if c.Gentoo.Profile != "" && profileCompatVariant(c.Gentoo.Stage3Variant) {
		// The stage3 variant and the eselect profile must agree on the init
		// system. Exotic variants (musl, hardened, x32, llvm, ...) have no
		// matching profile in the catalog, so the check is skipped for them.
		variantInit, profileInit := initName(c.UsesSystemd()), initName(ProfileUsesSystemd(c.Gentoo.Profile))
		if c.UsesSystemd() != ProfileUsesSystemd(c.Gentoo.Profile) {
			addf("stage3 variant %q uses %s but profile %q selects %s (init mismatch)",
				c.Gentoo.Stage3Variant, variantInit, c.Gentoo.Profile, profileInit)
		}
	}

	if c.Packages.KernelType != "bin" && c.Packages.KernelType != "source" {
		addf("invalid kernel type %q", c.Packages.KernelType)
	}
	if c.Gentoo.PortageSyncType != "git" && c.Gentoo.PortageSyncType != "rsync" {
		addf("invalid portage sync type %q", c.Gentoo.PortageSyncType)
	}
	return errs
}

// profileCompatVariant reports whether a stage3 variant has a counterpart in
// the Profiles catalog, i.e. it is a plain base or desktop variant whose init
// system is well-defined. Exotic variants (musl, hardened, x32, llvm,
// nomultilib, selinux) have no matching profile in the catalog, so init
// consistency with a selected profile cannot be enforced for them.
func profileCompatVariant(id string) bool {
	for _, v := range Stage3Variants {
		if v.ID != id {
			continue
		}
		for _, exotic := range []string{"musl", "hardened", "x32", "llvm", "nomultilib", "selinux"} {
			if strings.Contains(id, exotic) {
				return false
			}
		}
		return true
	}
	return false
}

func initName(systemd bool) string {
	if systemd {
		return "systemd"
	}
	return "OpenRC"
}

// Advisories returns non-fatal warnings about the configuration. Unlike
// Validate, these do not block installation but are surfaced to the user for
// attention (e.g. desktop alignment between the stage3 variant and profile).
func (c *Config) Advisories() []string {
	var warns []string
	if c.Gentoo.Profile == "" {
		return warns
	}
	variantDesktop := strings.Contains(c.Gentoo.Stage3Variant, "desktop")
	profileDesktop := strings.Contains(c.Gentoo.Profile, "/desktop")
	if variantDesktop != profileDesktop {
		warns = append(warns, "desktop stage3 variant and non-desktop profile (or vice versa) "+
			"are misaligned; the base package set may not match your intended desktop")
	}
	return warns
}
