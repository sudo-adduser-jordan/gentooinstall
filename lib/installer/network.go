package installer

import (
	"fmt"
	"strings"
)

// EnableService enables a service for the chosen init system
// (port of enable_service).
func EnableService(ctx *Context, name string) error {
	if ctx.Cfg.UsesSystemd() {
		return ctx.Runner.Try("systemctl", "enable", name)
	}
	return ctx.Runner.Try("rc-update", "add", name, "default")
}

const networkdTemplate = "[Match]\nName=%s\n\n[Network]\n%s"

// ConfigureNetworking sets up networking for the new system.
func ConfigureNetworking(ctx *Context) error {
	if ctx.Cfg.UsesSystemd() {
		if !ctx.Cfg.System.SystemdNetworkd {
			return nil
		}
		if err := EnableService(ctx, "systemd-networkd"); err != nil {
			return err
		}
		if err := EnableService(ctx, "systemd-resolved"); err != nil {
			return err
		}

		var network string
		if ctx.Cfg.System.SystemdNetworkdDHCP {
			network = fmt.Sprintf(networkdTemplate,
				ctx.Cfg.System.SystemdNetworkdInterfaceName, "DHCP=yes")
		} else {
			var sb strings.Builder
			for _, addr := range ctx.Cfg.System.SystemdNetworkdAddresses {
				sb.WriteString("Address=" + addr + "\n")
			}
			sb.WriteString("Gateway=" + ctx.Cfg.System.SystemdNetworkdGateway)
			network = fmt.Sprintf(networkdTemplate,
				ctx.Cfg.System.SystemdNetworkdInterfaceName, sb.String())
		}

		path := "/etc/systemd/network/20-wired.network"
		if err := ctx.mkdirAll("/etc/systemd/network", 0o755); err != nil {
			return err
		}
		if err := ctx.writeFile(path, []byte(network), 0o640); err != nil {
			return fmt.Errorf("could not write '%s': %w", path, err)
		}
		if out, err := ctx.Runner.QuietRun("chown", "root:systemd-network", path); err != nil {
			return fmt.Errorf("could not change owner of '%s':\n%s", path, out)
		}
		if out, err := ctx.Runner.QuietRun("chmod", "640", path); err != nil {
			return fmt.Errorf("could not change permissions of '%s':\n%s", path, out)
		}
		return nil
	}

	// OpenRC: install and enable dhcpcd.
	ctx.Runner.log("Installing dhcpcd")
	if err := ctx.Runner.Try("emerge", "--verbose", "net-misc/dhcpcd"); err != nil {
		return err
	}
	return EnableService(ctx, "dhcpcd")
}
