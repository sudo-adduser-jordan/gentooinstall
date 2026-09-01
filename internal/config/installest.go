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
func (c *Config) EstimateInstallSize() string {
	var giB float64
	if strings.Contains(c.Gentoo.Stage3Variant, "desktop") {
		giB += baseInstalledDesktopGiB
	} else {
		giB += baseInstalledMinimalGiB
	}

	switch c.Gentoo.PortageSyncType {
	case "rsync":
		giB += portageTreeGiB
	default:
		giB += portageTreeGiB
		if c.Gentoo.PortageGitFullHistory {
			giB += portageGitFullHistoryGiB
		}
	}

	if c.Packages.KernelType == "source" {
		giB += kernelSourceGiB
	} else {
		giB += kernelBinGiB
	}

	pkgs := len(c.ProfilePackages()) + len(c.Packages.Additional) +
		len(c.Packages.CustomPackages)
	giB += float64(pkgs) * perPackageGiB

	return fmt.Sprintf("~%.1f GiB", giB)
}

// EstimatePackageCount returns an approximation of the total number of
// packages installed on the new system: the stage3 base for the selected
// variant, the profile set, user-selected packages and fixed system pieces.
func (c *Config) EstimatePackageCount() int {
	base := basePackagesMinimal
	if strings.Contains(c.Gentoo.Stage3Variant, "desktop") {
		base = basePackagesDesktop
	}
	n := base + len(c.ProfilePackages()) + len(c.Packages.Additional) +
		len(c.Packages.CustomPackages)
	n++ // kernel
	if c.Packages.EnableSSHD {
		n++
	}
	if c.System.InitramfsSSHD {
		n++
	}
	return n
}
