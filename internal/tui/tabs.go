package tui

import (
	"fmt"
	"sort"
	"strings"

	"gentooinstall/assets"
	"gentooinstall/internal/config"

	"gentooinstall/internal/sysinfo"
)

const customDeviceMarker = "<enter custom path>"

func deviceOptions() []option {
	opts := []option{}
	for _, d := range sysinfo.Devices() {
		opts = append(opts, option{Value: d, Desc: ""})
	}
	return append(opts, option{Value: customDeviceMarker, Desc: "type a path manually"})
}

// devPick builds a choice field backed by /dev/disk/by-id plus a
// manual-path escape hatch (port of menu_select_device).
func devPick(label, help string, get func(*config.Config) string,
	set func(*config.Config, string)) *field {
	f := choice(label, deviceOptions, get, set, help)
	f.filter = true
	f.onPick = func(m *Model, fld *field, _ string) {
		m.openPicker(fld.label, fld.options(m.cfg), fld.getChoice(m.cfg), true,
			func(mm *Model, v string) {
				if v == customDeviceMarker {
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
				fld.setChoice(mm.cfg, v)
				mm.dirty = true
			})
	}
	return f
}

var schemeOptions = func() []option {
	var out []option
	for _, s := range config.Schemes {
		out = append(out, option{Value: s.Name, Desc: s.Desc})
	}
	return out
}

var stage3Options = func() []option {
	out := make([]option, 0, len(config.Stage3Variants))
	for _, v := range config.Stage3Variants {
		out = append(out, option{Value: v.ID, Desc: v.Description})
	}
	return out
}

var profileOptions = func() []option {
	out := make([]option, 0, len(config.Profiles))
	for _, p := range config.Profiles {
		out = append(out, option{Value: p.ID, Desc: p.Desc, primaryDesc: true})
	}
	return out
}

var makeConfOptions = func() []option {
	out := make([]option, 0, len(config.MakeConfOptions))
	for _, o := range config.MakeConfOptions {
		out = append(out, option{Value: o.Key, Desc: o.Desc})
	}
	return out
}

func staticOpts(vals ...string) []option {
	out := make([]option, 0, len(vals))
	for _, v := range vals {
		out = append(out, option{Value: v})
	}
	return out
}

var timezoneOptions = func() []option { return listToOpts(sysinfo.Timezones()) }
var keymapOptions = func() []option {
	k := sysinfo.Keymaps()
	if len(k) == 0 {
		k = sysinfo.FallbackKeymaps
	}
	return listToOpts(k)
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
	for _, x := range xs {
		out = append(out, option{Value: x})
	}
	return out
}

// overlayOptions lists the optional overlays available to enable. The
// "gentoo" repo is always on by default and is intentionally not offerable.
func overlayOptions() []option {
	out := make([]option, 0, len(config.Overlays))
	for _, o := range config.Overlays {
		out = append(out, option{Value: o.Name, Desc: o.Desc})
	}
	return out
}

func buildTabs(m *Model) []tabDef {

	schemeIs := func(schemes ...string) func(*config.Config) bool {
		return func(cc *config.Config) bool {
			for _, s := range schemes {
				if cc.Disk.Scheme == s {
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
			setChoice: func(cc *config.Config, v string) { cc.Disk.Scheme = v },
		},
		{
			label: "├ Boot type",
			help:  "Select whether to use EFI or BIOS boot.",
			kind:  kChoice,
			vis:   notCustom,
			options: func(*config.Config) []option {
				warnTxt := ""
				if !m.hasEFI {
					warnTxt = " (!! missing EFI support on this system !!)"
				}
				return []option{
					{Value: "efi", Desc: warnTxt},
					{Value: "bios"},
				}
			},
			getChoice: func(cc *config.Config) string { return cc.Disk.BootType },
			setChoice: func(cc *config.Config, v string) { cc.Disk.BootType = v },
		},
		func() *field {
			f := devPick("├ Boot device",
				"The device to use for the boot partition. For EFI systems this is "+
					"the efi partition. Must be formatted already.",
				func(cc *config.Config) string { return cc.Disk.BootDevice },
				func(cc *config.Config, v string) { cc.Disk.BootDevice = v })
			f.vis = schemeIs(config.SchemeExisting)
			return f
		}(),
		func() *field {
			f := devPick("└ Device",
				"The block device to which the layout will be applied.",
				func(cc *config.Config) string { return cc.Disk.Device },
				func(cc *config.Config, v string) { cc.Disk.Device = v })
			f.vis = schemeIs(config.SchemeClassic, config.SchemeExisting)
			return f
		}(),
		text("└ Devices",
			"The block devices to use for multi-disk layouts, separated by spaces.",
			func(cc *config.Config) string { return strings.Join(cc.Disk.Devices, " ") },
			func(cc *config.Config, v string) {
				cc.Disk.Devices = strings.Fields(v)
			}),
		sep("Swap"),
		toggle("├ Use swap", "Select whether or not to create a swap partition.",
			func(cc *config.Config) bool { return cc.Disk.UseSwap },
			func(cc *config.Config, v bool) { cc.Disk.UseSwap = v }),
		func() *field {
			f := text("└ Swap size",
				"Amount of swap to create, e.g. 8GiB or 4GB.",
				func(cc *config.Config) string { return cc.Disk.SwapSize },
				func(cc *config.Config, v string) { cc.Disk.SwapSize = v })
			f.vis = func(cc *config.Config) bool {
				return useSwap(cc) && cc.Disk.Scheme != config.SchemeExisting
			}
			return f
		}(),
		func() *field {
			f := devPick("│  └ Swap device", "The device to use as swap.",
				func(cc *config.Config) string { return cc.Disk.SwapDevice },
				func(cc *config.Config, v string) { cc.Disk.SwapDevice = v })
			f.vis = func(cc *config.Config) bool {
				return useSwap(cc) && cc.Disk.Scheme == config.SchemeExisting
			}
			return f
		}(),
		sep("Encryption & filesystems"),
		func() *field {
			f := choice("├ Root filesystem",
				func() []option { return staticOpts("ext4", "btrfs") },
				func(cc *config.Config) string { return cc.Disk.RootFS },
				func(cc *config.Config, v string) { cc.Disk.RootFS = v },
				"The filesystem used on the root partition.")
			f.vis = schemeIs(config.SchemeClassic, config.SchemeRaid0Luks, config.SchemeRaid1Luks)
			return f
		}(),
		func() *field {
			f := toggle("└ LUKS encryption",
				"Determines if LUKS will be used to encrypt your root partition. "+
					"You can export the desired encryption key via "+
					"GENTOO_INSTALL_ENCRYPTION_KEY before installing.",
				func(cc *config.Config) bool { return cc.Disk.UseLuks },
				func(cc *config.Config, v bool) { cc.Disk.UseLuks = v })
			f.vis = schemeIs(config.SchemeClassic, config.SchemeBtrfs,
				config.SchemeRaid0Luks, config.SchemeRaid1Luks)
			return f
		}(),
		func() *field {
			f := choice("├ ZFS pool type",
				func() []option { return staticOpts("standard", "custom") },
				func(cc *config.Config) string { return cc.Disk.ZFSPoolType },
				func(cc *config.Config, v string) { cc.Disk.ZFSPoolType = v },
				"'standard' sets up a default pool on all given devices; 'custom' is not "+
					"supported by gentooinstall and must be expressed via [disk.custom].")
			f.vis = schemeIs(config.SchemeZFSCentric)
			return f
		}(),
		func() *field {
			f := toggle("├ ZFS encryption",
				"Determines if ZFS native encryption will be used for the pool.",
				func(cc *config.Config) bool { return cc.Disk.ZFSEncrypt },
				func(cc *config.Config, v bool) { cc.Disk.ZFSEncrypt = v })
			f.vis = func(cc *config.Config) bool {
				return schemeIs(config.SchemeZFSCentric)(cc) && cc.Disk.ZFSPoolType == "standard"
			}
			return f
		}(),
		func() *field {
			f := toggle("├ Use ZFS compression",
				"Determines if compression should be enabled on the ZFS datasets.",
				func(cc *config.Config) bool { return cc.Disk.ZFSUseCompress },
				func(cc *config.Config, v bool) { cc.Disk.ZFSUseCompress = v })
			f.vis = func(cc *config.Config) bool {
				return schemeIs(config.SchemeZFSCentric)(cc) && cc.Disk.ZFSPoolType == "standard"
			}
			return f
		}(),
		func() *field {
			f := choice("│  └ Compression algorithm",
				func() []option {
					return staticOpts("on", "gzip", "lz4", "lzjb", "zle", "zstd", "zstd-fast")
				},
				func(cc *config.Config) string { return cc.Disk.ZFSCompression },
				func(cc *config.Config, v string) { cc.Disk.ZFSCompression = v },
				"'on' uses the default algorithm determined by ZFS.")
			f.vis = func(cc *config.Config) bool {
				return schemeIs(config.SchemeZFSCentric)(cc) &&
					cc.Disk.ZFSPoolType == "standard" && cc.Disk.ZFSUseCompress
			}
			return f
		}(),
		func() *field {
			f := choice("└ Btrfs raid type",
				func() []option { return staticOpts("raid0", "raid1") },
				func(cc *config.Config) string { return cc.Disk.BtrfsRaidType },
				func(cc *config.Config, v string) { cc.Disk.BtrfsRaidType = v },
				"Determines the data profile of the btrfs pool.")
			f.vis = schemeIs(config.SchemeBtrfs)
			return f
		}(),
	}}
	// visibility for the devices text field:
	diskTab.fields[5].vis = schemeIs(config.SchemeZFSCentric, config.SchemeBtrfs,
		config.SchemeRaid0Luks, config.SchemeRaid1Luks)

	systemTab := tabDef{name: "System", fields: []*field{
		text("Hostname",
			"The desired system hostname (RFC1123). Recorded in mdadm metadata too.",
			func(cc *config.Config) string { return cc.System.Hostname },
			func(cc *config.Config, v string) { cc.System.Hostname = strings.TrimSpace(v) }),
		filteredChoice("Timezone", timezoneOptions,
			func(cc *config.Config) string { return cc.System.Timezone },
			func(cc *config.Config, v string) { cc.System.Timezone = v },
			"The timezone for the new system."),
		filteredChoice("Keymap", keymapOptions,
			func(cc *config.Config) string { return cc.System.Keymap },
			func(cc *config.Config, v string) { cc.System.Keymap = v },
			"The default vconsole keymap for the system."),
		toggle("Different initramfs keymap",
			"Whether another keymap should be used for the initramfs.",
			func(cc *config.Config) bool { return cc.System.KeymapInitramfsOther },
			func(cc *config.Config, v bool) {
				cc.System.KeymapInitramfsOther = v
				if v && strings.TrimSpace(cc.System.KeymapInitramfs) == "" {
					cc.System.KeymapInitramfs = cc.System.Keymap
				}
			}),
		func() *field {
			f := filteredChoice("└ Keymap (initramfs)", keymapOptions,
				func(cc *config.Config) string { return cc.System.KeymapInitramfs },
				func(cc *config.Config, v string) { cc.System.KeymapInitramfs = v },
				"The vconsole keymap for the initramfs; important to unlock encrypted partitions when booting.")
			f.vis = func(cc *config.Config) bool { return cc.System.KeymapInitramfsOther }
			return f
		}(),
		func() *field {
			f := &field{
				label: "Locales",
				help: "The locales to generate for the new system (locale.gen lines). " +
					"For example 'en_US.UTF-8 UTF-8'.",
				kind:       kMultiChoice,
				options:    func(*config.Config) []option { return supportedLocaleOptions() },
				filter:     true,
				getStrings: func(cc *config.Config) []string { return cc.System.Locales },
				setStrings: func(cc *config.Config, v []string) { cc.System.Locales = v },
				vis:        func(*config.Config) bool { return true },
			}
			return f
		}(),
		filteredChoice("Default locale", systemLocaleOptions,
			func(cc *config.Config) string { return cc.System.Locale },
			func(cc *config.Config, v string) { cc.System.Locale = v },
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
			func(cc *config.Config, v bool) { cc.System.SystemdNetworkd = v }),
		func() *field {
			f := toggle("├ Enable sshd in initramfs",
				"Install and enable sshd in the initramfs to unlock encrypted "+
					"partitions via ssh (dracut-sshd).",
				func(cc *config.Config) bool { return cc.System.InitramfsSSHD },
				func(cc *config.Config, v bool) { cc.System.InitramfsSSHD = v })
			f.vis = netVis(func(cc *config.Config) bool { return cc.System.SystemdNetworkd })
			return f
		}(),
		func() *field {
			f := text("├ Interface name",
				"The network interface(s) to configure; may contain wildcards (en*).",
				func(cc *config.Config) string { return cc.System.SystemdNetworkdInterfaceName },
				func(cc *config.Config, v string) { cc.System.SystemdNetworkdInterfaceName = v })
			f.vis = netVis(func(cc *config.Config) bool { return cc.System.SystemdNetworkd })
			return f
		}(),
		func() *field {
			f := toggle("└ Use DHCP", "Use DHCP to obtain network configuration.",
				func(cc *config.Config) bool { return cc.System.SystemdNetworkdDHCP },
				func(cc *config.Config, v bool) { cc.System.SystemdNetworkdDHCP = v })
			f.vis = netVis(func(cc *config.Config) bool { return cc.System.SystemdNetworkd })
			return f
		}(),
		func() *field {
			f := text("   ├ Addresses",
				"Space separated addresses with CIDR mask for a static setup.",
				func(cc *config.Config) string {
					return strings.Join(cc.System.SystemdNetworkdAddresses, " ")
				},
				func(cc *config.Config, v string) {
					cc.System.SystemdNetworkdAddresses = strings.Fields(v)
				})
			f.vis = netVis(func(cc *config.Config) bool {
				return cc.System.SystemdNetworkd && !cc.System.SystemdNetworkdDHCP
			})
			return f
		}(),
		func() *field {
			f := text("   └ Gateway", "The gateway address for the network.",
				func(cc *config.Config) string { return cc.System.SystemdNetworkdGateway },
				func(cc *config.Config, v string) { cc.System.SystemdNetworkdGateway = v })
			f.vis = netVis(func(cc *config.Config) bool {
				return cc.System.SystemdNetworkd && !cc.System.SystemdNetworkdDHCP
			})
			return f
		}(),
	)

	gentooTab := tabDef{name: "Gentoo", fields: []*field{
		func() *field {
			f := choice("Stage3 init system", stage3Options,
				func(cc *config.Config) string { return cc.Gentoo.Stage3Variant },
				func(cc *config.Config, v string) { cc.Gentoo.Stage3Variant = v },
				"Select which stage3 tarball to use; implicitly determines systemd vs OpenRC.")
			f.summ = func(cc *config.Config) string {
				if cc.Gentoo.Stage3Variant == "" {
					return unsetStyle.Render("unset")
				}
				return profilePkgStyle.Render(cc.Gentoo.Stage3Variant)
			}
			return f
		}(),
		func() *field {
			f := filteredChoice("Profile (eselect)", profileOptions,
				func(cc *config.Config) string { return cc.Gentoo.Profile },
				func(cc *config.Config, v string) { cc.Gentoo.Profile = v },
				"Select the Gentoo profile (eselect profile). It tunes USE flags and "+
					"determines the base package set shown under Packages.")
			f.summ = func(cc *config.Config) string {
				if d := config.ProfileDesc(cc.Gentoo.Profile); d != "" {
					return profilePkgStyle.Render(d)
				}
				return unsetStyle.Render("unset")
			}
			return f
		}(),
		choice("Portage tree sync-type",
			func() []option { return staticOpts("git", "rsync") },
			func(cc *config.Config) string { return cc.Gentoo.PortageSyncType },
			func(cc *config.Config, v string) { cc.Gentoo.PortageSyncType = v },
			"The portage tree sync-type; git is generally preferred."),
		func() *field {
			f := toggle("├ Download full history",
				"Download full git history of the portage tree (1-2GB extra disk space).",
				func(cc *config.Config) bool { return cc.Gentoo.PortageGitFullHistory },
				func(cc *config.Config, v bool) { cc.Gentoo.PortageGitFullHistory = v })
			f.vis = func(cc *config.Config) bool { return cc.Gentoo.PortageSyncType == "git" }
			return f
		}(),
		func() *field {
			f := text("└ Git mirror",
				"The git endpoint used to sync the portage tree.",
				func(cc *config.Config) string { return cc.Gentoo.PortageGitMirror },
				func(cc *config.Config, v string) { cc.Gentoo.PortageGitMirror = v })
			f.vis = func(cc *config.Config) bool { return cc.Gentoo.PortageSyncType == "git" }
			return f
		}(),
		text("Gentoo mirror",
			"Initial gentoo mirror used during installation (full path incl. subdirectories).",
			func(cc *config.Config) string { return cc.Gentoo.Mirror },
			func(cc *config.Config, v string) { cc.Gentoo.Mirror = v }),
		choice("Gentoo arch",
			func() []option { return listToOpts(config.Archs) },
			func(cc *config.Config) string { return cc.Gentoo.Arch },
			func(cc *config.Config, v string) { cc.Gentoo.Arch = v },
			"Gentoo's architecture tag for the new system."),
		func() *field {
			f := choice("Gentoo sub-arch",
				func() []option { return listToOpts(config.SubArchs) },
				func(cc *config.Config) string { return cc.Gentoo.Subarch },
				func(cc *config.Config, v string) { cc.Gentoo.Subarch = v },
				"Sub-architecture tag, only relevant for x86.")
			f.vis = func(cc *config.Config) bool { return cc.Gentoo.Arch == "x86" }
			return f
		}(),
		toggle("Enable bleeding edge (~arch)",
			`Adds ACCEPT_KEYWORDS="~arch" at the end of installation.`,
			func(cc *config.Config) bool { return cc.Gentoo.UsePortageTesting },
			func(cc *config.Config, v bool) { cc.Gentoo.UsePortageTesting = v }),
		toggle("Run mirrorselect",
			"Determines if mirrorselect will be used to find the best gentoo mirror.",
			func(cc *config.Config) bool { return cc.Gentoo.SelectMirrors },
			func(cc *config.Config, v bool) { cc.Gentoo.SelectMirrors = v }),
		func() *field {
			f := toggle("└ Use large files",
				"Determines if mirrorselect uses large files (~10MB) to test mirrors.",
				func(cc *config.Config) bool { return cc.Gentoo.SelectMirrorsLargeFile },
				func(cc *config.Config, v bool) { cc.Gentoo.SelectMirrorsLargeFile = v })
			f.vis = func(cc *config.Config) bool { return cc.Gentoo.SelectMirrors }
			return f
		}(),
	}}

	packagesTab := tabDef{name: "Packages", fields: []*field{
		toggle("Enable sshd",
			"Install and enable sshd with a reasonably secure configuration.",
			func(cc *config.Config) bool { return cc.Packages.EnableSSHD },
			func(cc *config.Config, v bool) { cc.Packages.EnableSSHD = v }),
		toggle("Enable binary packages", "Use binary packages if available.",
			func(cc *config.Config) bool { return cc.Packages.EnableBinpkg },
			func(cc *config.Config, v bool) { cc.Packages.EnableBinpkg = v }),
		choice("Kernel type",
			func() []option {
				return []option{
					{Value: "bin", Desc: "Pre-built binary kernel (gentoo-kernel-bin)"},
					{Value: "source", Desc: "Build kernel from source (gentoo-kernel)"},
				}
			},
			func(cc *config.Config) string { return cc.Packages.KernelType },
			func(cc *config.Config, v string) { cc.Packages.KernelType = v },
			"Select which kernel package to install."),
		func() *field {
			f := toggle("└ Deblob kernel",
				"Remove binary firmware blobs from the source kernel and skip installing linux-firmware.",
				func(cc *config.Config) bool { return cc.Packages.KernelDeblob },
				func(cc *config.Config, v bool) { cc.Packages.KernelDeblob = v })
			f.vis = func(cc *config.Config) bool { return cc.Packages.KernelType == "source" }
			return f
		}(),
		multiText("Authorized keys (root)",
			"Authorized keys for ssh root login, one per line.",
			func(cc *config.Config) string {
				return strings.Join(cc.Packages.RootSSHAuthorizedKeys, "\n")
			},
			func(cc *config.Config, v string) {
				cc.Packages.RootSSHAuthorizedKeys = filterKeyLines(v)
			}),
		func() *field {
			f := &field{
				label: "Enable repositories/overlays",
				help: "Enable third-party ebuild repositories (overlays) via " +
					"eselect repository. The Gentoo 'gentoo' repo is always " +
					"available; enabling overlays adds their packages to the " +
					"'Additional packages' picker.",
				kind:       kMultiChoice,
				options:    func(*config.Config) []option { return overlayOptions() },
				filter:     true,
				getStrings: func(cc *config.Config) []string { return cc.Packages.EnablingRepos },
				setStrings: func(cc *config.Config, v []string) { cc.Packages.EnablingRepos = v },
				vis:        func(*config.Config) bool { return true },
			}
			return f
		}(),
		func() *field {
			f := &field{
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
				setStrings: func(cc *config.Config, v []string) { cc.Packages.Additional = v },
				vis:        func(*config.Config) bool { return true },
			}
			f.summ = func(cc *config.Config) string {
				return badgeStyle.Render(fmt.Sprintf("%d selected", len(cc.Packages.Additional)))
			}
			return f
		}(),
		func() *field {
			f := text("Custom atoms",
				"Additional portage package ATOMs to install that are not in the "+
					"enabled repositories' picker, delimited by spaces.",
				func(cc *config.Config) string { return strings.Join(cc.Packages.CustomPackages, " ") },
				func(cc *config.Config, v string) {
					cc.Packages.CustomPackages = strings.Fields(v)
				})
			f.summ = func(cc *config.Config) string {
				return badgeStyle.Render(fmt.Sprintf("%d custom", len(cc.Packages.CustomPackages)))
			}
			return f
		}(),
		sep("Portage Configuration"),
		func() *field {
			f := multiText("USE flags (package.use)",
				"Portage USE flags written to /etc/portage/package.use/user, one "+
					"entry per line. Each line selects flags for a package atom, "+
					"e.g. 'dev-libs/openssl -asm' or globally '*/* flag'.",
				func(cc *config.Config) string { return strings.Join(cc.Packages.UseFlags, "\n") },
				func(cc *config.Config, v string) {
					cc.Packages.UseFlags = filterKeyLines(v)
				})
			f.summ = func(cc *config.Config) string {
				if n := len(cc.Packages.UseFlags); n > 0 {
					return badgeStyle.Render(fmt.Sprintf("%d entries", n))
				}
				return unsetStyle.Render("none")
			}
			return f
		}(),
		readOnly("make.conf options",
			"Common /etc/portage/make.conf options to append at the end of the "+
				"installation. Space toggles; Enter applies.",
			func(cc *config.Config) string {
				return makeConfSummary(cc)
			},
			profilePkgStyle,
			func(m *Model, f *field, _ string) {
				m.openMultiPicker(f.label, makeConfOptions(), m.cfg.MakeConf.Options,
					func(mm *Model, vals []string) {
						mm.cfg.MakeConf.Options = vals
						mm.dirty = true
						mm.status = ""
					})
			}),
		func() *field {
			f := readOnly("edit make.conf",
				"View the effective /etc/portage/make.conf content (built-in entries, "+
					"picked options, and the freeform extra block). Editing is added later.",
				makeConfContentSummary,
				profilePkgStyle,
				func(m *Model, f *field, _ string) {
					m.openMakeConfView()
				})
			f.summ = func(*config.Config) string { return "" }
			return f
		}(),
		sep("Profile packages"),
		readOnly("Selected profile",
			"The Gentoo profile chosen on the Gentoo tab; its package set is shown below.",
			func(cc *config.Config) string {
				if d := config.ProfileDesc(cc.Gentoo.Profile); d != "" {
					return d
				}
				return cc.Gentoo.Profile
			},
			profilePkgStyle,
			func(m *Model, f *field, _ string) {
				// Bring up the profile selection menu, matching the Gentoo tab.
				m.openPicker(f.label, profileOptions(), m.cfg.Gentoo.Profile, true,
					func(mm *Model, v string) {
						mm.cfg.Gentoo.Profile = v
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
			func(m *Model, f *field, _ string) {
				m.openProfilePackages()
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

func filterKeyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
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
