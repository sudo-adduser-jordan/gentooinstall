package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gentooinstall/lib/pkglists"
)

const (
	SchemeClassic    = "classic_single_disk"
	SchemeExisting   = "existing_partitions"
	SchemeZFSCentric = "zfs_centric"
	SchemeBtrfs      = "btrfs_centric"
	SchemeRaid0Luks  = "raid0_luks"
	SchemeRaid1Luks  = "raid1_luks"
	SchemeCustom     = "custom"
)

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

type Stage3Variant struct {
	ID          string
	Description string
}

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

var Archs = []string{"x86", "amd64", "arm", "arm64"}

var SubArchs = []string{"i486", "i686"}

type Disk struct {
	Scheme     string   `toml:"scheme"`
	BootType   string   `toml:"boot_type"`
	Device     string   `toml:"device"`
	Devices    []string `toml:"devices"`
	BootDevice string   `toml:"boot_device"`

	UseSwap  bool   `toml:"use_swap"`
	SwapSize string `toml:"swap_size"`

	SwapDevice string `toml:"swap_device"`

	UseLuks bool   `toml:"use_luks"`
	RootFS  string `toml:"root_fs"`

	ZFSPoolType    string `toml:"zfs_pool_type"`
	ZFSEncrypt     bool   `toml:"zfs_encrypt"`
	ZFSUseCompress bool   `toml:"zfs_use_compression"`
	ZFSCompression string `toml:"zfs_compression"`
	BtrfsRaidType  string `toml:"btrfs_raid_type"`

	Custom []CustomAction `toml:"custom"`
}

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
	Compress string   `toml:"compress,omitempty"`
	RaidType string   `toml:"raid_type,omitempty"`
}

type System struct {
	Hostname             string   `toml:"hostname"`
	Timezone             string   `toml:"timezone"`
	Keymap               string   `toml:"keymap"`
	KeymapInitramfsOther bool     `toml:"keymap_initramfs_other"`
	KeymapInitramfs      string   `toml:"keymap_initramfs"`
	Locales              []string `toml:"locales"`
	Locale               string   `toml:"locale"`

	Systemd                      bool     `toml:"-"`
	SystemdNetworkd              bool     `toml:"systemd_networkd"`
	SystemdNetworkdInterfaceName string   `toml:"systemd_networkd_interface_name"`
	SystemdNetworkdDHCP          bool     `toml:"systemd_networkd_dhcp"`
	SystemdNetworkdAddresses     []string `toml:"systemd_networkd_addresses"`
	SystemdNetworkdGateway       string   `toml:"systemd_networkd_gateway"`
	InitramfsSSHD                bool     `toml:"initramfs_sshd"`
}

type Profile struct {
	ID       string
	Desc     string
	Packages []string
}

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

type Gentoo struct {
	Mirror                 string `toml:"mirror"`
	Arch                   string `toml:"arch"`
	Subarch                string `toml:"subarch"`
	Stage3Variant          string `toml:"stage3_variant"`
	Profile                string `toml:"profile"`
	PortageSyncType        string `toml:"portage_sync_type"`
	PortageGitFullHistory  bool   `toml:"portage_git_full_history"`
	PortageGitMirror       string `toml:"portage_git_mirror"`
	PortageRsyncMirror     string `toml:"portage_rsync_mirror"`
	UsePortageTesting      bool   `toml:"use_portage_testing"`
	SelectMirrors          bool   `toml:"select_mirrors"`
	SelectMirrorsLargeFile bool   `toml:"select_mirrors_large_file"`
}

type Packages struct {
	Additional            []string `toml:"additional"`
	CustomPackages        []string `toml:"custom_packages"`
	EnablingRepos         []string `toml:"enabling_repos"`
	EnableSSHD            bool     `toml:"enable_sshd"`
	EnableBinpkg          bool     `toml:"enable_binpkg"`
	KernelType            string   `toml:"kernel_type"`
	KernelDeblob          bool     `toml:"kernel_deblob"`
	InstallFirmware       bool     `toml:"install_firmware"`
	FirmwareSections      []string `toml:"firmware_sections"`
	RootSSHAuthorizedKeys []string `toml:"ssh_authorized_keys"`
	UseFlags              []string `toml:"use_flags"`
}

type MakeConf struct {
	Options []string `toml:"options"`
	Extra   string   `toml:"extra"`
}

type MakeConfOption struct {
	Key  string
	Desc string
	Line string
}

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

func LookupMakeConfOption(key string) *MakeConfOption {
	for index := range MakeConfOptions {
		if MakeConfOptions[index].Key == key {
			return &MakeConfOptions[index]
		}
	}
	return nil
}

type Config struct {
	Disk     Disk     `toml:"disk"`
	System   System   `toml:"system"`
	Gentoo   Gentoo   `toml:"gentoo"`
	Packages Packages `toml:"packages"`
	MakeConf MakeConf `toml:"makeconf"`
}

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

func LookupProfile(id string) *Profile {
	for index := range Profiles {
		if Profiles[index].ID == id {
			return &Profiles[index]
		}
	}
	return nil
}

type Repo struct {
	Name     string
	Desc     string
	IndexURL string
}

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

const MainRepoIndexURL = "https://mirrors.kernel.org/gentoo-portage"

type FirmwareSection struct {
	Name string
	Desc string
	Dirs []string
}

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

const DefaultPortageRsyncMirror = "rsync://rsync.gentoo.org/gentoo-portage"

func LookupOverlay(name string) *Repo {
	for index := range Overlays {
		if Overlays[index].Name == name {
			return &Overlays[index]
		}
	}
	return nil
}

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

func ProfileDesc(id string) string {
	if profile := LookupProfile(id); profile != nil {
		return profile.Desc
	}
	return ""
}

func ProfileUsesSystemd(id string) bool {
	return strings.HasSuffix(id, "/systemd")
}

func (cfg *Config) ProfilePackages() []string {
	profile := LookupProfile(cfg.Gentoo.Profile)
	if profile == nil {
		return nil
	}
	return profile.Packages
}

func (cfg *Config) UsesSystemd() bool { return strings.Contains(cfg.Gentoo.Stage3Variant, "systemd") }

func (cfg *Config) UsesMusl() bool { return strings.Contains(cfg.Gentoo.Stage3Variant, "musl") }

func (cfg *Config) Stage3BaseName() string {
	return fmt.Sprintf("stage3-%s-%s", cfg.Gentoo.Arch, cfg.Gentoo.Stage3Variant)
}

func (cfg *Config) Stage3BaseNameCustom() string {
	if strings.Contains(cfg.Gentoo.Stage3Variant, "x32") {
		return "stage3-" + cfg.Gentoo.Stage3Variant
	}
	return fmt.Sprintf("stage3-%s-%s", cfg.Gentoo.Subarch, cfg.Gentoo.Stage3Variant)
}

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
	}

	if cfg.Gentoo.Profile != "" && LookupProfile(cfg.Gentoo.Profile) == nil {
		addf("unknown profile %q", cfg.Gentoo.Profile)
	} else if cfg.Gentoo.Profile != "" && profileCompatVariant(cfg.Gentoo.Stage3Variant) {
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
