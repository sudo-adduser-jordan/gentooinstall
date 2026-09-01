package installer

import (
	"fmt"
	"os"
	"strings"
)

// EnableService enables a service for the chosen init system
// (port of enable_service).
func EnableService(c *Context, name string) error {
	if c.Cfg.UsesSystemd() {
		return c.R.Try("systemctl", "enable", name)
	}
	return c.R.Try("rc-update", "add", name, "default")
}

const networkdTemplate = "[Match]\nName=%s\n\n[Network]\n%s"

// ConfigureNetworking sets up networking for the new system.
func ConfigureNetworking(c *Context) error {
	if c.Cfg.UsesSystemd() {
		if !c.Cfg.System.SystemdNetworkd {
			return nil
		}
		if err := EnableService(c, "systemd-networkd"); err != nil {
			return err
		}
		if err := EnableService(c, "systemd-resolved"); err != nil {
			return err
		}

		var network string
		if c.Cfg.System.SystemdNetworkdDHCP {
			network = fmt.Sprintf(networkdTemplate,
				c.Cfg.System.SystemdNetworkdInterfaceName, "DHCP=yes")
		} else {
			var sb strings.Builder
			for _, addr := range c.Cfg.System.SystemdNetworkdAddresses {
				sb.WriteString("Address=" + addr + "\n")
			}
			sb.WriteString("Gateway=" + c.Cfg.System.SystemdNetworkdGateway)
			network = fmt.Sprintf(networkdTemplate,
				c.Cfg.System.SystemdNetworkdInterfaceName, sb.String())
		}

		path := "/etc/systemd/network/20-wired.network"
		if err := os.MkdirAll("/etc/systemd/network", 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(network), 0o640); err != nil {
			return fmt.Errorf("could not write '%s': %w", path, err)
		}
		if out, err := c.R.QuietRun("chown", "root:systemd-network", path); err != nil {
			return fmt.Errorf("could not change owner of '%s':\n%s", path, out)
		}
		if out, err := c.R.QuietRun("chmod", "640", path); err != nil {
			return fmt.Errorf("could not change permissions of '%s':\n%s", path, out)
		}
		return nil
	}

	// OpenRC: install and enable dhcpcd.
	c.R.log("Installing dhcpcd")
	if err := c.R.Try("emerge", "--verbose", "net-misc/dhcpcd"); err != nil {
		return err
	}
	return EnableService(c, "dhcpcd")
}
