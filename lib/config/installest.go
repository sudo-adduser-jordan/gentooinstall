package config

import (
	"fmt"
	"strings"
)

// Constants used by the install overview. Every figure is an approximation
// of a typical Gentoo installation (there is no dependency resolution), so
// both estimates are intentionally surfaced as "~" values.

const (
	baseInstalledMinimalGiB = 1.6
	baseInstalledDesktopGiB = 2.4

	portageTreeGiB           = 0.4 // rsync or shallow git mirror
	portageGitFullHistoryGiB = 2.0

	kernelBinGiB    = 0.5
	kernelSourceGiB = 2.5

	perPackageGiB = 0.1
)

const (
	basePackagesMinimal = 350
	basePackagesDesktop = 850
)

// EstimateInstallSize returns a human-readable approximation of the total
// installed size for the current configuration.
func (cfg *Config) EstimateInstallSize() string {
	var giB float64
	if strings.Contains(cfg.Gentoo.Stage3Variant, "desktop") {
		giB += baseInstalledDesktopGiB
	} else {
		giB += baseInstalledMinimalGiB
	}

	switch cfg.Gentoo.PortageSyncType {
	case "rsync":
		giB += portageTreeGiB
	default:
		giB += portageTreeGiB
		if cfg.Gentoo.PortageGitFullHistory {
			giB += portageGitFullHistoryGiB
		}
	}

	if cfg.Packages.KernelType == "source" {
		giB += kernelSourceGiB
	} else {
		giB += kernelBinGiB
	}

	pkgs := len(cfg.ProfilePackages()) + len(cfg.Packages.Additional) +
		len(cfg.Packages.CustomPackages)
	giB += float64(pkgs) * perPackageGiB

	return fmt.Sprintf("~%.1f GiB", giB)
}

// EstimatePackageCount returns an approximation of the total number of
// packages installed on the new system: the stage3 base for the selected
// variant, the profile set, user-selected packages and fixed system pieces.
func (cfg *Config) EstimatePackageCount() int {
	base := basePackagesMinimal
	if strings.Contains(cfg.Gentoo.Stage3Variant, "desktop") {
		base = basePackagesDesktop
	}
	count := base + len(cfg.ProfilePackages()) + len(cfg.Packages.Additional) +
		len(cfg.Packages.CustomPackages)
	count++ // kernel
	if cfg.Packages.EnableSSHD {
		count++
	}
	if cfg.System.InitramfsSSHD {
		count++
	}
	return count
}
