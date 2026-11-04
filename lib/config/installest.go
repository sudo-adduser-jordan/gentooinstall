package config

import (
	"fmt"
	"strings"
)

const (
	baseInstalledMinimalGiB = 1.6
	baseInstalledDesktopGiB = 2.4

	portageTreeGiB           = 0.4
	portageGitFullHistoryGiB = 2.0

	kernelBinGiB    = 0.5
	kernelSourceGiB = 2.5

	perPackageGiB = 0.1
)

const (
	basePackagesMinimal = 350
	basePackagesDesktop = 850
)

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

func (cfg *Config) EstimatePackageCount() int {
	base := basePackagesMinimal
	if strings.Contains(cfg.Gentoo.Stage3Variant, "desktop") {
		base = basePackagesDesktop
	}
	count := base + len(cfg.ProfilePackages()) + len(cfg.Packages.Additional) +
		len(cfg.Packages.CustomPackages)
	count++
	if cfg.Packages.EnableSSHD {
		count++
	}
	if cfg.System.InitramfsSSHD {
		count++
	}
	return count
}
