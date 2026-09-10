// Package config defines the gentooinstall configuration model and its TOML
// representation (gentoo.toml).
package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gentooinstall/lib/pkglists"
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
	PortageRsyncMirror     string `toml:"portage_rsync_mirror"`
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
	InstallFirmware       bool     `toml:"install_firmware"`
	FirmwareSections      []string `toml:"firmware_sections"` // dirs to keep; empty = install everything
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
	for index := range MakeConfOptions {
		if MakeConfOptions[index].Key == key {
			return &MakeConfOptions[index]
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
// (port of load_default_config). EFI is the default boot type on modern
// machines; it falls back to BIOS only when the live system reports no EFI.
func Default(hasEFI bool) *Config {
	boot := "efi"
	if !hasEFI {
		boot = "bios"
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
			Mirror:             "https://mirror.leaseweb.com/gentoo",
			Arch:               "amd64",
			Stage3Variant:      "systemd",
			PortageSyncType:    "git",
			PortageGitMirror:   "https://anongit.gentoo.org/git/repo/sync/gentoo.git",
			PortageRsyncMirror: DefaultPortageRsyncMirror,
			UsePortageTesting:  true,
		},
		Packages: Packages{
			EnableSSHD:      true,
			EnableBinpkg:    true,
			KernelType:      "bin",
			InstallFirmware: true,
		},
	}
}

// LookupProfile returns the profile with the given id, or nil.
func LookupProfile(id string) *Profile {
	for index := range Profiles {
		if Profiles[index].ID == id {
			return &Profiles[index]
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
	// data/repos/<name>.packages by scripts/packages.sh and embedded at
	// build time (see lib/pkglists). It is not used at runtime.
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

// FirmwareSection is one selectable vendor category of the
// sys-kernel/linux-firmware package. Dirs are the top-level directories under
// /lib/firmware that the category covers and that are pruned when the
// category is not selected.
type FirmwareSection struct {
	Name string
	Desc string
	Dirs []string
}

// FirmwareSections lists the linux-firmware vendor categories offered in the
// TUI. Selecting none installs the whole package; selecting some installs
// only the chosen top-level firmware directories. Entries whose Description
// carries "(Wi-Fi)" cover wireless-firmware directories and are shown with
// that hint in the picker. The union of Dirs covers every firmware directory
// shipped by the package.
var FirmwareSections = []FirmwareSection{
	{Name: "amd", Desc: "AMD/ATI GPU, CPU microcode, platform",
		Dirs: []string{"amd", "amdgpu", "amd-ucode", "amdtee", "r128", "radeon"}},
	{Name: "intel", Desc: "Intel GPU, Wi-Fi/Bluetooth, Ethernet, SAS (Wi-Fi)",
		Dirs: []string{"e100", "i915", "intel", "isci", "ixp4xx", "xe"}},
	{Name: "nvidia", Desc: "NVIDIA GPU (GSP-RM)", Dirs: []string{"nvidia"}},
	{Name: "broadcom", Desc: "Broadcom/Cypress Wi-Fi, Bluetooth, NICs (Wi-Fi)",
		Dirs: []string{"bnx2", "bnx2x", "brcm", "cypress", "tigon"}},
	{Name: "qualcomm", Desc: "Qualcomm/Atheros Wi-Fi, Bluetooth, SoC (Wi-Fi)",
		Dirs: []string{"ar3k", "ath10k", "ath11k", "ath12k", "ath6k", "ath9k_htc",
			"qca", "qcom"}},
	{Name: "realtek", Desc: "Realtek Wi-Fi, Bluetooth, Ethernet (Wi-Fi)",
		Dirs: []string{"realtek", "rtl_bt", "rtl_nic", "rtlwifi", "rtw88", "rtw89"}},
	{Name: "mediatek", Desc: "MediaTek/Airoha Wi-Fi and Bluetooth (Wi-Fi)",
		Dirs: []string{"airoha", "mediatek"}},
	{Name: "marvell", Desc: "Marvell/NXP Wi-Fi, Bluetooth, networking (Wi-Fi)",
		Dirs: []string{"libertas", "mrvl", "mwl8k", "mwlwifi", "nxp"}},
	{Name: "siliconlabs", Desc: "Silicon Labs WFX Wi-Fi (Wi-Fi)", Dirs: []string{"wfx"}},
	{Name: "ti", Desc: "Texas Instruments Wi-Fi and SoC (Wi-Fi)",
		Dirs: []string{"ti", "ti-connectivity", "ti-keystone"}},
	{Name: "wifi-other", Desc: "Other Wi-Fi: Atmel, carl9170, Redpine (Wi-Fi)",
		Dirs: []string{"atmel", "carl9170fw", "rsi"}},
	{Name: "network-enterprise",
		Desc: "Mellanox, Chelsio, QLogic, Cavium, Netronome and other NICs/serial",
		Dirs: []string{"3com", "aeonsemi", "cavium", "cxgb3", "cxgb4", "kaweth",
			"liquidio", "mellanox", "myricom", "netronome", "qed", "qlogic",
			"slicoss", "sun", "sxg", "tehuti", "vxge"}},
	{Name: "arm-soc", Desc: "ARM Mali GPU and SoCs (Amlogic, Rockchip, NXP, ...)",
		Dirs: []string{"amlogic", "amphion", "arm", "cadence", "dpaa2", "imx",
			"inside-secure", "meson", "microchip", "powervr", "rockchip"}},
	{Name: "laptop-ish", Desc: "Laptop sensor hubs (Intel ISH on Dell/HP/Lenovo)",
		Dirs: []string{"dell", "HP", "LENOVO"}},
	{Name: "scsi-storage", Desc: "SCSI/UAS controllers and card readers",
		Dirs: []string{"adaptec", "advansys", "ene-ub6250"}},
	{Name: "audio", Desc: "Sound cards (Cirrus, Emagic, ESS, Korg, Creative, Yamaha)",
		Dirs: []string{"cirrus", "emi26", "emi62", "ess", "korg", "sb16", "yamaha"}},
	{Name: "usb-serial", Desc: "USB serial adapters (Keyspan, Edgeport, Moxa)",
		Dirs: []string{"edgeport", "keyspan", "keyspan_pda", "moxa", "ositech"}},
	{Name: "tv-video", Desc: "DVB receivers, video cameras, USB modems",
		Dirs: []string{"av7110", "cis", "cnm", "cpia2", "dabusb", "dsp56k", "go7007",
			"ttusb-budget", "ueagle-atm", "vicam"}},
	{Name: "misc", Desc: "Everything else (ATUSB radio, Matrox G200, YAM modem)",
		Dirs: []string{"atusb", "matrox", "yam"}},
}

// WirelessFirmwareDir reports whether the firmware directory carries wireless
// (Wi-Fi) firmware, used to hint those rows in the picker.
func WirelessFirmwareDir(dir string) bool {
	switch dir {
	case "ath10k", "ath11k", "ath12k", "ath6k", "ath9k_htc", "atmel", "brcm",
		"carl9170fw", "cypress", "intel", "libertas", "mediatek", "mwl8k",
		"mwlwifi", "nxp", "realtek", "rsi", "rtl_bt", "rtlwifi", "rtw88",
		"rtw89", "ti-connectivity", "wfx":
		return true
	}
	return false
}

// firmwareModuleDirs maps loaded kernel module names to the top-level
// linux-firmware directories that hold their firmware. It is used to infer
// which firmware directories a machine needs from the modules its kernel has
// loaded.
var firmwareModuleDirs = map[string][]string{
	"3c59x":         {"3com"},
	"aacraid":       {"adaptec"},
	"amdgpu":        {"amdgpu"},
	"radeon":        {"radeon"},
	"nouveau":       {"nvidia"},
	"nvidia":        {"nvidia"},
	"e100":          {"e100"},
	"e1000":         {"e100"},
	"e1000e":        {"e100"},
	"igb":           {"e100"},
	"ixgbe":         {"e100"},
	"i40e":          {"e100"},
	"fm10k":         {"e100"},
	"i915":          {"i915"},
	"xe":            {"xe"},
	"isci":          {"isci"},
	"ixp4xx_eth":    {"ixp4xx"},
	"iwlwifi":       {"intel"},
	"iwlmvm":        {"intel"},
	"iwldvm":        {"intel"},
	"intel_ish_ipc": {"dell", "HP", "LENOVO"},
	"brcmfmac":      {"brcm"},
	"b43":           {"brcm"},
	"b43legacy":     {"brcm"},
	"bnx2":          {"bnx2"},
	"bnx2x":         {"bnx2x"},
	"tg3":           {"tigon"},
	"ath3k":         {"ar3k"},
	"btqca":         {"qca"},
	"ath6kl":        {"ath6k"},
	"ath9k_htc":     {"ath9k_htc"},
	"ath10k_pci":    {"ath10k"},
	"ath10k_sdio":   {"ath10k"},
	"ath11k_pci":    {"ath11k"},
	"ath12k_pci":    {"ath12k"},
	"wcn36xx":       {"qcom"},
	"carl9170":      {"carl9170fw"},
	"at76c50x_usb":  {"atmel"},
	"rsi_sdio":      {"rsi"},
	"rsi_usb":       {"rsi"},
	"r8169":         {"rtl_nic"},
	"r8168":         {"rtl_nic"},
	"rtlwifi":       {"rtlwifi"},
	"rtl8xxxu":      {"rtlwifi"},
	"rtl8192ce":     {"rtlwifi"},
	"rtl8192cu":     {"rtlwifi"},
	"rtl8192se":     {"rtlwifi"},
	"rtw88":         {"rtw88"},
	"rtw89":         {"rtw89"},
	"btusb":         {"rtl_bt"},
	"mt76x0u":       {"mediatek"},
	"mt76x2u":       {"mediatek"},
	"mt7601u":       {"mediatek"},
	"mt7921e":       {"mediatek"},
	"mt7921u":       {"mediatek"},
	"mt7915e":       {"mediatek"},
	"mt7996e":       {"mediatek"},
	"libertas_sdio": {"libertas"},
	"libertas_tf":   {"libertas"},
	"mwifiex":       {"mrvl"},
	"btmrvl":        {"mrvl"},
	"mwl8k":         {"mwl8k"},
	"wfx":           {"wfx"},
	"wlcore":        {"ti-connectivity"},
	"wl12xx":        {"ti-connectivity"},
	"wl18xx":        {"ti-connectivity"},
	"mlx5_core":     {"mellanox"},
	"mlx4_core":     {"mellanox"},
	"mlx4_en":       {"mellanox"},
	"cxgb3":         {"cxgb3"},
	"cxgb4":         {"cxgb4"},
	"cxgb4vf":       {"cxgb4"},
	"qed":           {"qed"},
	"qlcnic":        {"qlogic"},
	"qla3xxx":       {"qlogic"},
	"liquidio":      {"liquidio"},
	"nfp":           {"netronome"},
	"myri10ge":      {"myricom"},
	"snd_korg1212":  {"korg"},
	"snd_emi26":     {"emi26"},
	"snd_emi62":     {"emi62"},
	"snd_sb16":      {"sb16"},
	"keyspan":       {"keyspan"},
	"keyspan_pda":   {"keyspan_pda"},
	"io_edgeport":   {"edgeport"},
	"mxuport":       {"moxa"},
	"av7110":        {"av7110"},
	"go7007":        {"go7007"},
	"cpia2":         {"cpia2"},
	"vicam":         {"vicam"},
	"ueagle-atm":    {"ueagle-atm"},
	"atusb":         {"atusb"},
	"yam":           {"yam"},
	"mgag200":       {"matrox"},
}

// DetectedFirmwareFor returns the firmware directories a machine needs given
// its loaded kernel modules and CPU vendor string ("AuthenticAMD" pulls in
// the AMD CPU microcode). The result is sorted.
func DetectedFirmwareFor(modules []string, cpuVendor string) []string {
	seen := map[string]bool{}
	for _, module := range modules {
		for _, dir := range firmwareModuleDirs[strings.ToLower(strings.TrimSpace(module))] {
			seen[dir] = true
		}
	}
	if strings.TrimSpace(cpuVendor) == "AuthenticAMD" {
		seen["amd-ucode"] = true
	}
	if len(seen) == 0 {
		return nil
	}
	det := make([]string, 0, len(seen))
	for dir := range seen {
		det = append(det, dir)
	}
	sort.Strings(det)
	return det
}

// DetectedFirmwareSections probes the running system (/proc/modules and
// /proc/cpuinfo) and returns the firmware directories its kernel already
// loaded drivers for. It is used to pre-select the firmware-sections picker.
func DetectedFirmwareSections() []string {
	var modules []string
	if data, err := os.ReadFile("/proc/modules"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if fields := strings.Fields(line); len(fields) > 0 {
				modules = append(modules, fields[0])
			}
		}
	}
	cpuVendor := ""
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if fields := strings.Fields(line); len(fields) >= 3 && fields[0] == "vendor_id" {
				cpuVendor = fields[2]
				break
			}
		}
	}
	return DetectedFirmwareFor(modules, cpuVendor)
}

// DefaultPortageRsyncMirror is the canonical rsync URI used when no custom
// portage_rsync_mirror is configured.
const DefaultPortageRsyncMirror = "rsync://rsync.gentoo.org/gentoo-portage"

// LookupOverlay returns the overlay with the given name, or nil.
func LookupOverlay(name string) *Repo {
	for index := range Overlays {
		if Overlays[index].Name == name {
			return &Overlays[index]
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
		for _, atom := range atoms {
			if seen[atom] {
				continue
			}
			seen[atom] = true
			all = append(all, atom)
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
	if profile := LookupProfile(id); profile != nil {
		return profile.Desc
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
func (cfg *Config) ProfilePackages() []string {
	profile := LookupProfile(cfg.Gentoo.Profile)
	if profile == nil {
		return nil
	}
	return profile.Packages
}

// UsesSystemd reports whether the selected stage3 variant uses systemd.
func (cfg *Config) UsesSystemd() bool { return strings.Contains(cfg.Gentoo.Stage3Variant, "systemd") }

// UsesMusl reports whether the selected stage3 variant is musl-based.
func (cfg *Config) UsesMusl() bool { return strings.Contains(cfg.Gentoo.Stage3Variant, "musl") }

// Stage3BaseName returns "stage3-$arch-$variant".
func (cfg *Config) Stage3BaseName() string {
	return fmt.Sprintf("stage3-%s-%s", cfg.Gentoo.Arch, cfg.Gentoo.Stage3Variant)
}

// Stage3BaseNameCustom handles the x32 / x86-subarch naming special case.
func (cfg *Config) Stage3BaseNameCustom() string {
	if strings.Contains(cfg.Gentoo.Stage3Variant, "x32") {
		return "stage3-" + cfg.Gentoo.Stage3Variant
	}
	return fmt.Sprintf("stage3-%s-%s", cfg.Gentoo.Subarch, cfg.Gentoo.Stage3Variant)
}

// Stage3BaseNameFinal picks the basename actually used for downloads.
func (cfg *Config) Stage3BaseNameFinal() string {
	if (cfg.Gentoo.Arch == "amd64" && strings.Contains(cfg.Gentoo.Stage3Variant, "x32")) ||
		(cfg.Gentoo.Arch == "x86" && cfg.Gentoo.Subarch != "") {
		return cfg.Stage3BaseNameCustom()
	}
	return cfg.Stage3BaseName()
}

var hostnameRe = regexp.MustCompile(
	`^(([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]*[a-zA-Z0-9])\.)*([A-Za-z0-9]|[A-Za-z0-9][A-Za-z0-9\-]*[A-Za-z0-9])$`)

var keymapRe = regexp.MustCompile(`^[0-9A-Za-z-]*$`)

// Validate checks semantic correctness of the configuration
// (everything checkable without touching disks).
func (cfg *Config) Validate() []error {
	var errs []error
	addf := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if !keymapRe.MatchString(cfg.System.Keymap) {
		addf("KEYMAP %q contains invalid characters", cfg.System.Keymap)
	}
	if !hostnameRe.MatchString(cfg.System.Hostname) {
		addf("%q is not a valid hostname", cfg.System.Hostname)
	}

	switch cfg.System.Locale {
	case "":
		addf("no default locale set")
	}
	if len(cfg.System.Locales) == 0 {
		addf("no locales to generate")
	}

	validScheme := false
	for _, scheme := range Schemes {
		if scheme.Name == cfg.Disk.Scheme {
			validScheme = true
			break
		}
	}
	if !validScheme {
		addf("unknown partitioning scheme %q", cfg.Disk.Scheme)
		return errs
	}

	if cfg.Disk.Scheme != SchemeCustom {
		if cfg.Disk.BootType != "efi" && cfg.Disk.BootType != "bios" {
			addf("invalid boot type %q", cfg.Disk.BootType)
		}
		multi := map[string]bool{
			SchemeZFSCentric: true, SchemeBtrfs: true,
			SchemeRaid0Luks: true, SchemeRaid1Luks: true,
		}[cfg.Disk.Scheme]
		single := map[string]bool{SchemeClassic: true, SchemeExisting: true}[cfg.Disk.Scheme]

		if single && cfg.Disk.Device == "" {
			addf("no device configured for scheme %q", cfg.Disk.Scheme)
		}
		if multi && len(cfg.Disk.Devices) == 0 {
			addf("no devices configured for scheme %q", cfg.Disk.Scheme)
		}
		if multi && cfg.Disk.Scheme != SchemeZFSCentric && cfg.Disk.Scheme != SchemeBtrfs &&
			len(cfg.Disk.Devices) < 2 {
			addf("scheme %q needs at least 2 devices", cfg.Disk.Scheme)
		}
		if cfg.Disk.Scheme == SchemeExisting && cfg.Disk.BootDevice == "" {
			addf("existing_partitions needs boot_device")
		}
		if cfg.Disk.UseSwap && cfg.Disk.SwapSize == "" && cfg.Disk.Scheme != SchemeExisting {
			addf("swap enabled but no swap size given")
		}
	}

	switch cfg.Gentoo.Arch {
	case "x86":
	default:
		if cfg.Gentoo.Subarch != "" {
			addf("subarch only valid for x86")
		}
	}
	found := false
	for _, arch := range Archs {
		if arch == cfg.Gentoo.Arch {
			found = true
		}
	}
	if !found {
		addf("unknown architecture %q", cfg.Gentoo.Arch)
	}

	vfound := false
	for _, variant := range Stage3Variants {
		if variant.ID == cfg.Gentoo.Stage3Variant {
			vfound = true
		}
	}
	if !vfound {
		addf("unknown stage3 variant %q", cfg.Gentoo.Stage3Variant)
	} else if cfg.UsesSystemd() != cfg.System.SystemdNetworkd {
		// networkd options only apply to systemd; not fatal but suspicious.
	}

	if cfg.Gentoo.Profile != "" && LookupProfile(cfg.Gentoo.Profile) == nil {
		addf("unknown profile %q", cfg.Gentoo.Profile)
	} else if cfg.Gentoo.Profile != "" && profileCompatVariant(cfg.Gentoo.Stage3Variant) {
		// The stage3 variant and the eselect profile must agree on the init
		// system. Exotic variants (musl, hardened, x32, llvm, ...) have no
		// matching profile in the catalog, so the check is skipped for them.
		variantInit, profileInit := initName(cfg.UsesSystemd()), initName(ProfileUsesSystemd(cfg.Gentoo.Profile))
		if cfg.UsesSystemd() != ProfileUsesSystemd(cfg.Gentoo.Profile) {
			addf("stage3 variant %q uses %s but profile %q selects %s (init mismatch)",
				cfg.Gentoo.Stage3Variant, variantInit, cfg.Gentoo.Profile, profileInit)
		}
	}

	if cfg.Packages.KernelType != "bin" && cfg.Packages.KernelType != "source" {
		addf("invalid kernel type %q", cfg.Packages.KernelType)
	}
	if cfg.Gentoo.PortageSyncType != "git" && cfg.Gentoo.PortageSyncType != "rsync" {
		addf("invalid portage sync type %q", cfg.Gentoo.PortageSyncType)
	}
	return errs
}

// profileCompatVariant reports whether a stage3 variant has a counterpart in
// the Profiles catalog, i.e. it is a plain base or desktop variant whose init
// system is well-defined. Exotic variants (musl, hardened, x32, llvm,
// nomultilib, selinux) have no matching profile in the catalog, so init
// consistency with a selected profile cannot be enforced for them.
func profileCompatVariant(id string) bool {
	for _, variant := range Stage3Variants {
		if variant.ID != id {
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
func (cfg *Config) Advisories() []string {
	var warns []string
	if cfg.Gentoo.Profile == "" {
		return warns
	}
	variantDesktop := strings.Contains(cfg.Gentoo.Stage3Variant, "desktop")
	profileDesktop := strings.Contains(cfg.Gentoo.Profile, "/desktop")
	if variantDesktop != profileDesktop {
		warns = append(warns, "desktop stage3 variant and non-desktop profile (or vice versa) "+
			"are misaligned; the base package set may not match your intended desktop")
	}
	return warns
}
