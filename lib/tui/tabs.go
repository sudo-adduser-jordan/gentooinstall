package tui

import (
	"fmt"
	"sort"
	"strings"

	"gentooinstall/assets"
	"gentooinstall/lib/config"

	"gentooinstall/lib/sysinfo"
)

const customDeviceMarker = "<enter custom path>"

// BlockDevices lists the block devices offered by the disk picker. It is
// overridable so tests can exercise the no-devices fallback.
var BlockDevices = sysinfo.Devices

// qemuFallbackDevices are offered when no block device could be discovered,
// so a QEMU VM whose disk driver did not load can still target a device.
var qemuFallbackDevices = []option{
	{Value: "/dev/sda", Desc: "QEMU default (IDE/SATA)"},
	{Value: "/dev/vda", Desc: "QEMU virtio-blk"},
}

func deviceOptions() []option {
	opts := []option{}
	for _, device := range BlockDevices() {
		opts = append(opts, option{Value: device, Desc: ""})
	}
	if len(opts) == 0 {
		opts = append(opts, qemuFallbackDevices...)
	}
	return append(opts, option{Value: customDeviceMarker, Desc: "type a path manually"})
}

// devPick builds a choice field backed by /dev/disk/by-id plus a
// manual-path escape hatch (port of menu_select_device).
func devPick(label, help string, get func(*config.Config) string,
	set func(*config.Config, string)) *field {
	devField := choice(label, deviceOptions, get, set, help)
	devField.filter = true
	devField.onPick = func(model *Model, fld *field, _ string) {
		model.openPicker(fld.label, fld.options(model.cfg), fld.getChoice(model.cfg), true,
			func(mm *Model, value string) {
				if value == customDeviceMarker {
					prev := fld.getChoice(mm.cfg)
					mm.openText("Enter device path", prev, false, func(m2 *Model, path string) {
						path = strings.TrimSpace(path)
						if path == "" {
							return
						}
						if _, err := exists(path); err != nil {
							m2.setStatusErr(err.Error())
							// keep editing until valid or cancelled
							m2.openText("Enter device path (does not exist!)",
								path, false, func(m3 *Model, p2 string) {
									p2 = strings.TrimSpace(p2)
									if p2 == "" {
										return
									}
									if _, err := exists(p2); err != nil {
										m3.setStatusErr(err.Error())
										return
									}
									fld.setChoice(m3.cfg, p2)
									m3.dirty = true
								})
							return
						}
						fld.setChoice(mm.cfg, sysinfo.CanonicalizeDevice(path))
						mm.dirty = true
					})
					return
				}
				fld.setChoice(mm.cfg, value)
				mm.dirty = true
			})
	}
	return devField
}

var schemeOptions = func() []option {
	var out []option
	for _, scheme := range config.Schemes {
		out = append(out, option{Value: scheme.Name, Desc: scheme.Desc})
	}
	return out
}

var stage3Options = func() []option {
	out := make([]option, 0, len(config.Stage3Variants))
	for _, variant := range config.Stage3Variants {
		out = append(out, option{Value: variant.ID, Desc: variant.Description})
	}
	return out
}

var profileOptions = func() []option {
	out := make([]option, 0, len(config.Profiles))
	for _, profile := range config.Profiles {
		out = append(out, option{Value: profile.ID, Desc: profile.Desc, primaryDesc: true})
	}
	return out
}

var makeConfOptions = func() []option {
	out := make([]option, 0, len(config.MakeConfOptions))
	for _, opt := range config.MakeConfOptions {
		out = append(out, option{Value: opt.Key, Desc: opt.Desc})
	}
	return out
}

func staticOpts(vals ...string) []option {
	out := make([]option, 0, len(vals))
	for _, value := range vals {
		out = append(out, option{Value: value})
	}
	return out
}

var timezoneOptions = func() []option { return listToOpts(sysinfo.Timezones()) }
var keymapOptions = func() []option {
	keymaps := sysinfo.Keymaps()
	if len(keymaps) == 0 {
		keymaps = sysinfo.FallbackKeymaps
	}
	return listToOpts(keymaps)
}
var systemLocaleOptions = func() []option {
	locs, err := sysinfo.SystemLocales()
	if err != nil || len(locs) == 0 {
		locs = []string{"C.UTF-8", "C", "en_US.utf8"}
	}
	return listToOpts(locs)
}
var supportedLocaleOptions = func() []option { return listToOpts(assets.SupportedLocales()) }

func listToOpts(xs []string) []option {
	out := make([]option, 0, len(xs))
	for _, item := range xs {
		out = append(out, option{Value: item})
	}
	return out
}

// overlayOptions lists the optional overlays available to enable. The
// "gentoo" repo is always on by default and is intentionally not offerable.
func overlayOptions() []option {
	out := make([]option, 0, len(config.Overlays))
	for _, overlay := range config.Overlays {
		out = append(out, option{Value: overlay.Name, Desc: overlay.Desc})
	}
	return out
}

// firmwareSectionOptions lists the selectable sys-kernel/linux-firmware
// categories. WiFi entries carry a "(Wi-Fi)" hint in their description.
func firmwareSectionOptions() []option {
	out := make([]option, 0, len(config.FirmwareSections))
	for _, section := range config.FirmwareSections {
		out = append(out, option{Value: section.Name, Desc: section.Desc})
	}
	return out
}

func buildTabs(model *Model) []tabDef {

	schemeIs := func(schemes ...string) func(*config.Config) bool {
		return func(cc *config.Config) bool {
			for _, scheme := range schemes {
				if cc.Disk.Scheme == scheme {
					return true
				}
			}
			return false
		}
	}
	notCustom := func(cc *config.Config) bool { return cc.Disk.Scheme != config.SchemeCustom }
	useSwap := func(cc *config.Config) bool { return notCustom(cc) && cc.Disk.UseSwap }

	diskTab := tabDef{name: "Disk", fields: []*field{
		sep("Partitioning"),
		{
			label: "Partitioning scheme",
			help: "Select which partitioning scheme you want to follow. All options " +
				"support EFI/BIOS, swap and some form of encryption (luks/zfs).",
			kind:      kChoice,
			options:   func(*config.Config) []option { return schemeOptions() },
			getChoice: func(cc *config.Config) string { return cc.Disk.Scheme },
			setChoice: func(cc *config.Config, value string) { cc.Disk.Scheme = value },
		},
		{
			label: "├ Boot type",
			help:  "Select whether to use EFI or BIOS boot.",
			kind:  kChoice,
			vis:   notCustom,
			options: func(*config.Config) []option {
				warnTxt := ""
				if !model.hasEFI {
					warnTxt = " (!! missing EFI support on this system !!)"
				}
				return []option{
					{Value: "efi", Desc: warnTxt},
					{Value: "bios"},
				}
			},
			getChoice: func(cc *config.Config) string { return cc.Disk.BootType },
			setChoice: func(cc *config.Config, value string) { cc.Disk.BootType = value },
		},
		func() *field {
			field := devPick("├ Boot device",
				"The device to use for the boot partition. For EFI systems this is "+
					"the efi partition. Must be formatted already.",
				func(cc *config.Config) string { return cc.Disk.BootDevice },
				func(cc *config.Config, value string) { cc.Disk.BootDevice = value })
			field.vis = schemeIs(config.SchemeExisting)
			return field
		}(),
		func() *field {
			field := devPick("└ Device",
				"The block device to which the layout will be applied.",
				func(cc *config.Config) string { return cc.Disk.Device },
				func(cc *config.Config, value string) { cc.Disk.Device = value })
			field.vis = schemeIs(config.SchemeClassic, config.SchemeExisting)
			return field
		}(),
		text("└ Devices",
			"The block devices to use for multi-disk layouts, separated by spaces.",
			func(cc *config.Config) string { return strings.Join(cc.Disk.Devices, " ") },
			func(cc *config.Config, value string) {
				cc.Disk.Devices = strings.Fields(value)
			}),
		sep("Swap"),
		toggle("├ Use swap", "Select whether or not to create a swap partition.",
			func(cc *config.Config) bool { return cc.Disk.UseSwap },
			func(cc *config.Config, value bool) { cc.Disk.UseSwap = value }),
		func() *field {
			field := text("└ Swap size",
				"Amount of swap to create, e.g. 8GiB or 4GB.",
				func(cc *config.Config) string { return cc.Disk.SwapSize },
				func(cc *config.Config, value string) { cc.Disk.SwapSize = value })
			field.vis = func(cc *config.Config) bool {
				return useSwap(cc) && cc.Disk.Scheme != config.SchemeExisting
			}
			return field
		}(),
		func() *field {
			field := devPick("│  └ Swap device", "The device to use as swap.",
				func(cc *config.Config) string { return cc.Disk.SwapDevice },
				func(cc *config.Config, value string) { cc.Disk.SwapDevice = value })
			field.vis = func(cc *config.Config) bool {
				return useSwap(cc) && cc.Disk.Scheme == config.SchemeExisting
			}
			return field
		}(),
		sep("Encryption & filesystems"),
		func() *field {
			field := choice("├ Root filesystem",
				func() []option { return staticOpts("ext4", "btrfs") },
				func(cc *config.Config) string { return cc.Disk.RootFS },
				func(cc *config.Config, value string) { cc.Disk.RootFS = value },
				"The filesystem used on the root partition.")
			field.vis = schemeIs(config.SchemeClassic, config.SchemeRaid0Luks, config.SchemeRaid1Luks)
			return field
		}(),
		func() *field {
			field := toggle("└ LUKS encryption",
				"Determines if LUKS will be used to encrypt your root partition. "+
					"You can export the desired encryption key via "+
					"GENTOO_INSTALL_ENCRYPTION_KEY before installing.",
				func(cc *config.Config) bool { return cc.Disk.UseLuks },
				func(cc *config.Config, value bool) { cc.Disk.UseLuks = value })
			field.vis = schemeIs(config.SchemeClassic, config.SchemeBtrfs,
				config.SchemeRaid0Luks, config.SchemeRaid1Luks)
			return field
		}(),
		func() *field {
			field := choice("├ ZFS pool type",
				func() []option { return staticOpts("standard", "custom") },
				func(cc *config.Config) string { return cc.Disk.ZFSPoolType },
				func(cc *config.Config, value string) { cc.Disk.ZFSPoolType = value },
				"'standard' sets up a default pool on all given devices; 'custom' is not "+
					"supported by gentooinstall and must be expressed via [disk.custom].")
			field.vis = schemeIs(config.SchemeZFSCentric)
			return field
		}(),
		func() *field {
			field := toggle("├ ZFS encryption",
				"Determines if ZFS native encryption will be used for the pool.",
				func(cc *config.Config) bool { return cc.Disk.ZFSEncrypt },
				func(cc *config.Config, value bool) { cc.Disk.ZFSEncrypt = value })
			field.vis = func(cc *config.Config) bool {
				return schemeIs(config.SchemeZFSCentric)(cc) && cc.Disk.ZFSPoolType == "standard"
			}
			return field
		}(),
		func() *field {
			field := toggle("├ Use ZFS compression",
				"Determines if compression should be enabled on the ZFS datasets.",
				func(cc *config.Config) bool { return cc.Disk.ZFSUseCompress },
				func(cc *config.Config, value bool) { cc.Disk.ZFSUseCompress = value })
			field.vis = func(cc *config.Config) bool {
				return schemeIs(config.SchemeZFSCentric)(cc) && cc.Disk.ZFSPoolType == "standard"
			}
			return field
		}(),
		func() *field {
			field := choice("│  └ Compression algorithm",
				func() []option {
					return staticOpts("on", "gzip", "lz4", "lzjb", "zle", "zstd", "zstd-fast")
				},
				func(cc *config.Config) string { return cc.Disk.ZFSCompression },
				func(cc *config.Config, value string) { cc.Disk.ZFSCompression = value },
				"'on' uses the default algorithm determined by ZFS.")
			field.vis = func(cc *config.Config) bool {
				return schemeIs(config.SchemeZFSCentric)(cc) &&
					cc.Disk.ZFSPoolType == "standard" && cc.Disk.ZFSUseCompress
			}
			return field
		}(),
		func() *field {
			field := choice("└ Btrfs raid type",
				func() []option { return staticOpts("raid0", "raid1") },
				func(cc *config.Config) string { return cc.Disk.BtrfsRaidType },
				func(cc *config.Config, value string) { cc.Disk.BtrfsRaidType = value },
				"Determines the data profile of the btrfs pool.")
			field.vis = schemeIs(config.SchemeBtrfs)
			return field
		}(),
	}}
	// visibility for the devices text field:
	diskTab.fields[5].vis = schemeIs(config.SchemeZFSCentric, config.SchemeBtrfs,
		config.SchemeRaid0Luks, config.SchemeRaid1Luks)

	systemTab := tabDef{name: "System", fields: []*field{
		text("Hostname",
			"The desired system hostname (RFC1123). Recorded in mdadm metadata too.",
			func(cc *config.Config) string { return cc.System.Hostname },
			func(cc *config.Config, value string) { cc.System.Hostname = strings.TrimSpace(value) }),
		filteredChoice("Timezone", timezoneOptions,
			func(cc *config.Config) string { return cc.System.Timezone },
			func(cc *config.Config, value string) { cc.System.Timezone = value },
			"The timezone for the new system."),
		filteredChoice("Keymap", keymapOptions,
			func(cc *config.Config) string { return cc.System.Keymap },
			func(cc *config.Config, value string) { cc.System.Keymap = value },
			"The default vconsole keymap for the system."),
		toggle("Different initramfs keymap",
			"Whether another keymap should be used for the initramfs.",
			func(cc *config.Config) bool { return cc.System.KeymapInitramfsOther },
			func(cc *config.Config, value bool) {
				cc.System.KeymapInitramfsOther = value
				if value && strings.TrimSpace(cc.System.KeymapInitramfs) == "" {
					cc.System.KeymapInitramfs = cc.System.Keymap
				}
			}),
		func() *field {
			field := filteredChoice("└ Keymap (initramfs)", keymapOptions,
				func(cc *config.Config) string { return cc.System.KeymapInitramfs },
				func(cc *config.Config, value string) { cc.System.KeymapInitramfs = value },
				"The vconsole keymap for the initramfs; important to unlock encrypted partitions when booting.")
			field.vis = func(cc *config.Config) bool { return cc.System.KeymapInitramfsOther }
			return field
		}(),
		func() *field {
			field := &field{
				label: "Locales",
				help: "The locales to generate for the new system (locale.gen lines). " +
					"For example 'en_US.UTF-8 UTF-8'.",
				kind:       kMultiChoice,
				options:    func(*config.Config) []option { return supportedLocaleOptions() },
				filter:     true,
				getStrings: func(cc *config.Config) []string { return cc.System.Locales },
				setStrings: func(cc *config.Config, value []string) { cc.System.Locales = value },
				vis:        func(*config.Config) bool { return true },
			}
			return field
		}(),
		filteredChoice("Default locale", systemLocaleOptions,
			func(cc *config.Config) string { return cc.System.Locale },
			func(cc *config.Config, value string) { cc.System.Locale = value },
			"The default locale; remember to generate it in the list above."),
	}}

	networkTab := tabDef{name: "Network", fields: []*field{}}
	netVis := func(extra func(*config.Config) bool) func(*config.Config) bool {
		return func(cc *config.Config) bool {
			return cc.UsesSystemd() && (extra == nil || extra(cc))
		}
	}
	networkTab.fields = append(networkTab.fields,
		toggle("Configure network (systemd-networkd)",
			"Enable systemd-networkd to configure networking on the new system.",
			func(cc *config.Config) bool { return cc.System.SystemdNetworkd },
			func(cc *config.Config, value bool) { cc.System.SystemdNetworkd = value }),
		func() *field {
			field := toggle("├ Enable sshd in initramfs",
				"Install and enable sshd in the initramfs to unlock encrypted "+
					"partitions via ssh (dracut-sshd).",
				func(cc *config.Config) bool { return cc.System.InitramfsSSHD },
				func(cc *config.Config, value bool) { cc.System.InitramfsSSHD = value })
			field.vis = netVis(func(cc *config.Config) bool { return cc.System.SystemdNetworkd })
			return field
		}(),
		func() *field {
			field := text("├ Interface name",
				"The network interface(s) to configure; may contain wildcards (en*).",
				func(cc *config.Config) string { return cc.System.SystemdNetworkdInterfaceName },
				func(cc *config.Config, value string) { cc.System.SystemdNetworkdInterfaceName = value })
			field.vis = netVis(func(cc *config.Config) bool { return cc.System.SystemdNetworkd })
			return field
		}(),
		func() *field {
			field := toggle("└ Use DHCP", "Use DHCP to obtain network configuration.",
				func(cc *config.Config) bool { return cc.System.SystemdNetworkdDHCP },
				func(cc *config.Config, value bool) { cc.System.SystemdNetworkdDHCP = value })
			field.vis = netVis(func(cc *config.Config) bool { return cc.System.SystemdNetworkd })
			return field
		}(),
		func() *field {
			field := text("   ├ Addresses",
				"Space separated addresses with CIDR mask for a static setup.",
				func(cc *config.Config) string {
					return strings.Join(cc.System.SystemdNetworkdAddresses, " ")
				},
				func(cc *config.Config, value string) {
					cc.System.SystemdNetworkdAddresses = strings.Fields(value)
				})
			field.vis = netVis(func(cc *config.Config) bool {
				return cc.System.SystemdNetworkd && !cc.System.SystemdNetworkdDHCP
			})
			return field
		}(),
		func() *field {
			field := text("   └ Gateway", "The gateway address for the network.",
				func(cc *config.Config) string { return cc.System.SystemdNetworkdGateway },
				func(cc *config.Config, value string) { cc.System.SystemdNetworkdGateway = value })
			field.vis = netVis(func(cc *config.Config) bool {
				return cc.System.SystemdNetworkd && !cc.System.SystemdNetworkdDHCP
			})
			return field
		}(),
	)

	gentooTab := tabDef{name: "Gentoo", fields: []*field{
		func() *field {
			field := choice("Stage3 init system", stage3Options,
				func(cc *config.Config) string { return cc.Gentoo.Stage3Variant },
				func(cc *config.Config, value string) { cc.Gentoo.Stage3Variant = value },
				"Select which stage3 tarball to use; implicitly determines systemd vs OpenRC.")
			field.summ = func(cc *config.Config) string {
				if cc.Gentoo.Stage3Variant == "" {
					return unsetStyle.Render("unset")
				}
				return profilePkgStyle.Render(cc.Gentoo.Stage3Variant)
			}
			return field
		}(),
		func() *field {
			field := filteredChoice("Profile (eselect)", profileOptions,
				func(cc *config.Config) string { return cc.Gentoo.Profile },
				func(cc *config.Config, value string) { cc.Gentoo.Profile = value },
				"Select the Gentoo profile (eselect profile). It tunes USE flags and "+
					"determines the base package set shown under Packages.")
			field.summ = func(cc *config.Config) string {
				if desc := config.ProfileDesc(cc.Gentoo.Profile); desc != "" {
					return profilePkgStyle.Render(desc)
				}
				return unsetStyle.Render("unset")
			}
			return field
		}(),
		choice("Portage tree sync-type",
			func() []option { return staticOpts("git", "rsync") },
			func(cc *config.Config) string { return cc.Gentoo.PortageSyncType },
			func(cc *config.Config, value string) { cc.Gentoo.PortageSyncType = value },
			"The portage tree sync-type; git is generally preferred."),
		func() *field {
			field := toggle("├ Download full history",
				"Download full git history of the portage tree (1-2GB extra disk space).",
				func(cc *config.Config) bool { return cc.Gentoo.PortageGitFullHistory },
				func(cc *config.Config, value bool) { cc.Gentoo.PortageGitFullHistory = value })
			field.vis = func(cc *config.Config) bool { return cc.Gentoo.PortageSyncType == "git" }
			return field
		}(),
		func() *field {
			field := text("└ Git mirror",
				"The git endpoint used to sync the portage tree.",
				func(cc *config.Config) string { return cc.Gentoo.PortageGitMirror },
				func(cc *config.Config, value string) { cc.Gentoo.PortageGitMirror = value })
			field.vis = func(cc *config.Config) bool { return cc.Gentoo.PortageSyncType == "git" }
			return field
		}(),
		func() *field {
			field := text("└ Rsync mirror",
				"The rsync endpoint used to sync the portage tree.",
				func(cc *config.Config) string { return cc.Gentoo.PortageRsyncMirror },
				func(cc *config.Config, value string) { cc.Gentoo.PortageRsyncMirror = value })
			field.vis = func(cc *config.Config) bool { return cc.Gentoo.PortageSyncType == "rsync" }
			return field
		}(),
		func() *field {
			field := text("Gentoo mirror",
				"Initial gentoo mirror used during installation (full path incl. subdirectories).",
				func(cc *config.Config) string { return cc.Gentoo.Mirror },
				func(cc *config.Config, value string) { cc.Gentoo.Mirror = value })
			field.watchMirror = true
			return field
		}(),
		choice("Gentoo arch",
			func() []option { return listToOpts(config.Archs) },
			func(cc *config.Config) string { return cc.Gentoo.Arch },
			func(cc *config.Config, value string) { cc.Gentoo.Arch = value },
			"Gentoo's architecture tag for the new system."),
		func() *field {
			field := choice("Gentoo sub-arch",
				func() []option { return listToOpts(config.SubArchs) },
				func(cc *config.Config) string { return cc.Gentoo.Subarch },
				func(cc *config.Config, value string) { cc.Gentoo.Subarch = value },
				"Sub-architecture tag, only relevant for x86.")
			field.vis = func(cc *config.Config) bool { return cc.Gentoo.Arch == "x86" }
			return field
		}(),
		toggle("Enable bleeding edge (~arch)",
			`Adds ACCEPT_KEYWORDS="~arch" at the end of installation.`,
			func(cc *config.Config) bool { return cc.Gentoo.UsePortageTesting },
			func(cc *config.Config, value bool) { cc.Gentoo.UsePortageTesting = value }),
		toggle("Run mirrorselect",
			"Determines if mirrorselect will be used to find the best gentoo mirror.",
			func(cc *config.Config) bool { return cc.Gentoo.SelectMirrors },
			func(cc *config.Config, value bool) { cc.Gentoo.SelectMirrors = value }),
		func() *field {
			field := toggle("└ Use large files",
				"Determines if mirrorselect uses large files (~10MB) to test mirrors.",
				func(cc *config.Config) bool { return cc.Gentoo.SelectMirrorsLargeFile },
				func(cc *config.Config, value bool) { cc.Gentoo.SelectMirrorsLargeFile = value })
			field.vis = func(cc *config.Config) bool { return cc.Gentoo.SelectMirrors }
			return field
		}(),
	}}

	packagesTab := tabDef{name: "Packages", fields: []*field{
		toggle("Enable sshd",
			"Install and enable sshd with a reasonably secure configuration.",
			func(cc *config.Config) bool { return cc.Packages.EnableSSHD },
			func(cc *config.Config, value bool) { cc.Packages.EnableSSHD = value }),
		toggle("Enable binary packages", "Use binary packages if available.",
			func(cc *config.Config) bool { return cc.Packages.EnableBinpkg },
			func(cc *config.Config, value bool) { cc.Packages.EnableBinpkg = value }),
		choice("Kernel type",
			func() []option {
				return []option{
					{Value: "bin", Desc: "Pre-built binary kernel (gentoo-kernel-bin)"},
					{Value: "source", Desc: "Build kernel from source (gentoo-kernel)"},
				}
			},
			func(cc *config.Config) string { return cc.Packages.KernelType },
			func(cc *config.Config, value string) { cc.Packages.KernelType = value },
			"Select which kernel package to install."),
		func() *field {
			field := toggle("└ Deblob kernel",
				"Remove binary firmware blobs from the source kernel and skip installing linux-firmware.",
				func(cc *config.Config) bool { return cc.Packages.KernelDeblob },
				func(cc *config.Config, value bool) { cc.Packages.KernelDeblob = value })
			field.vis = func(cc *config.Config) bool { return cc.Packages.KernelType == "source" }
			return field
		}(),
		func() *field {
			field := toggle("Install linux-firmware (non-free)",
				"Install the sys-kernel/linux-firmware package containing proprietary "+
					"(non-free) hardware firmware blobs. Turn this off to skip the "+
					"package entirely. Selecting nothing under 'Firmware sections' "+
					"installs the full set.",
				func(cc *config.Config) bool { return cc.Packages.InstallFirmware },
				func(cc *config.Config, value bool) { cc.Packages.InstallFirmware = value })
			field.vis = func(cc *config.Config) bool { return !cc.Packages.KernelDeblob }
			return field
		}(),
		func() *field {
			field := &field{
				label: "└ Firmware sections",
				help: "Limit the installed sys-kernel/linux-firmware categories " +
					"(Wi-Fi entries are hinted with '(Wi-Fi)'). This installs the full " +
					"package and prunes unselected firmware directories from " +
					"/lib/firmware. Selecting nothing installs everything.",
				kind: kMultiChoice,
				options: func(*config.Config) []option {
					return firmwareSectionOptions()
				},
				filter:     true,
				getStrings: func(cc *config.Config) []string { return cc.Packages.FirmwareSections },
				setStrings: func(cc *config.Config, value []string) {
					cc.Packages.FirmwareSections = value
				},
				vis: func(cc *config.Config) bool {
					return !cc.Packages.KernelDeblob && cc.Packages.InstallFirmware
				},
			}
			field.summ = func(cc *config.Config) string {
				if len(cc.Packages.FirmwareSections) == 0 {
					return badgeStyle.Render("all")
				}
				return badgeStyle.Render(fmt.Sprintf("%d selected", len(cc.Packages.FirmwareSections)))
			}
			return field
		}(),
		multiText("Authorized keys (root)",
			"Authorized keys for ssh root login, one per line.",
			func(cc *config.Config) string {
				return strings.Join(cc.Packages.RootSSHAuthorizedKeys, "\n")
			},
			func(cc *config.Config, value string) {
				cc.Packages.RootSSHAuthorizedKeys = filterKeyLines(value)
			}),
		func() *field {
			field := &field{
				label: "Enable repositories/overlays",
				help: "Enable third-party ebuild repositories (overlays) via " +
					"eselect repository. The Gentoo 'gentoo' repo is always " +
					"available; enabling overlays adds their packages to the " +
					"'Additional packages' picker.",
				kind:       kMultiChoice,
				options:    func(*config.Config) []option { return overlayOptions() },
				filter:     true,
				getStrings: func(cc *config.Config) []string { return cc.Packages.EnablingRepos },
				setStrings: func(cc *config.Config, value []string) { cc.Packages.EnablingRepos = value },
				vis:        func(*config.Config) bool { return true },
			}
			return field
		}(),
		func() *field {
			field := &field{
				label: "Additional packages",
				help: "Portage package atoms to install, picked from the enabled " +
					"repositories. Search to filter; Space toggles selection. " +
					"Anything not listed can be typed in the 'Custom atoms' field.",
				kind: kMultiChoice,
				options: func(cc *config.Config) []option {
					return listToOpts(config.RepoPackages(cc.Packages.EnablingRepos))
				},
				filter:     true,
				getStrings: func(cc *config.Config) []string { return cc.Packages.Additional },
				setStrings: func(cc *config.Config, value []string) { cc.Packages.Additional = value },
				vis:        func(*config.Config) bool { return true },
			}
			field.summ = func(cc *config.Config) string {
				return badgeStyle.Render(fmt.Sprintf("%d selected", len(cc.Packages.Additional)))
			}
			return field
		}(),
		func() *field {
			field := text("Custom atoms",
				"Additional portage package ATOMs to install that are not in the "+
					"enabled repositories' picker, delimited by spaces.",
				func(cc *config.Config) string { return strings.Join(cc.Packages.CustomPackages, " ") },
				func(cc *config.Config, value string) {
					cc.Packages.CustomPackages = strings.Fields(value)
				})
			field.summ = func(cc *config.Config) string {
				return badgeStyle.Render(fmt.Sprintf("%d custom", len(cc.Packages.CustomPackages)))
			}
			return field
		}(),
		sep("Portage Configuration"),
		func() *field {
			field := multiText("USE flags (package.use)",
				"Portage USE flags written to /etc/portage/package.use/user, one "+
					"entry per line. Each line selects flags for a package atom, "+
					"e.g. 'dev-libs/openssl -asm' or globally '*/* flag'.",
				func(cc *config.Config) string { return strings.Join(cc.Packages.UseFlags, "\n") },
				func(cc *config.Config, value string) {
					cc.Packages.UseFlags = filterKeyLines(value)
				})
			field.summ = func(cc *config.Config) string {
				if count := len(cc.Packages.UseFlags); count > 0 {
					return badgeStyle.Render(fmt.Sprintf("%d entries", count))
				}
				return unsetStyle.Render("none")
			}
			return field
		}(),
		readOnly("make.conf options",
			"Common /etc/portage/make.conf options to append at the end of the "+
				"installation. Space toggles; Enter applies.",
			func(cc *config.Config) string {
				return makeConfSummary(cc)
			},
			profilePkgStyle,
			func(model *Model, field *field, _ string) {
				model.openMultiPicker(field.label, makeConfOptions(), model.cfg.MakeConf.Options,
					func(mm *Model, vals []string) {
						mm.cfg.MakeConf.Options = vals
						mm.dirty = true
						mm.status = ""
					})
			}),
		func() *field {
			field := readOnly("edit make.conf",
				"View the effective /etc/portage/make.conf content (built-in entries, "+
					"picked options, and the freeform extra block). Editing is added later.",
				makeConfContentSummary,
				profilePkgStyle,
				func(model *Model, field *field, _ string) {
					model.openMakeConfView()
				})
			field.summ = func(*config.Config) string { return "" }
			return field
		}(),
		sep("Profile packages"),
		readOnly("Selected profile",
			"The Gentoo profile chosen on the Gentoo tab; its package set is shown below.",
			func(cc *config.Config) string {
				if desc := config.ProfileDesc(cc.Gentoo.Profile); desc != "" {
					return desc
				}
				return cc.Gentoo.Profile
			},
			profilePkgStyle,
			func(model *Model, field *field, _ string) {
				// Bring up the profile selection menu, matching the Gentoo tab.
				model.openPicker(field.label, profileOptions(), model.cfg.Gentoo.Profile, true,
					func(mm *Model, value string) {
						mm.cfg.Gentoo.Profile = value
						mm.dirty = true
						mm.status = ""
					})
			}),
		readOnly("Installed by profile",
			"Packages pulled in by the selected profile. These are read-only; "+
				"add your own in the 'Additional packages' field above.",
			func(cc *config.Config) string {
				return fmt.Sprintf("%d packages", len(cc.ProfilePackages()))
			},
			profilePkgStyle,
			func(model *Model, field *field, _ string) {
				model.openProfilePackages()
			}),
	}}

	installTab := tabDef{name: "Install", render: renderInstallTab}

	return []tabDef{
		diskTab,
		systemTab,
		networkTab,
		gentooTab,
		packagesTab,
		installTab,
	}
}

func makeConfSummary(cc *config.Config) string {
	if len(cc.MakeConf.Options) == 0 {
		return unsetStyle.Render("none")
	}
	return badgeStyle.Render(fmt.Sprintf("%d selected", len(cc.MakeConf.Options)))
}

// makeConfContentSummary shows the freeform extra block (or hints at the
// effective content) for the make.conf viewer row.
func makeConfContentSummary(cc *config.Config) string {
	extra := strings.TrimSpace(cc.MakeConf.Extra)
	if extra != "" {
		first, _, _ := strings.Cut(extra, "\n")
		return badgeStyle.Render(first)
	}
	if len(cc.MakeConf.Options) == 0 {
		return unsetStyle.Render("none")
	}
	return badgeStyle.Render(fmt.Sprintf("%d option", len(cc.MakeConf.Options)))
}

func filterKeyLines(input string) []string {
	var out []string
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

func exists(path string) (bool, error) {
	if _, err := osStat(path); err != nil {
		return false, fmt.Errorf("the device %s does not exist", path)
	}
	return true, nil
}

const overviewHelp = "This overview summarizes the current configuration state."
