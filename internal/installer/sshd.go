package installer

import (
	"fmt"
	"os"
	"strings"

	"gentooinstall/assets"
)

// EnableSSHD installs the hardened sshd configuration and enables it.
func EnableSSHD(c *Context) error {
	c.R.log("Installing and enabling sshd")
	if err := os.MkdirAll("/etc/ssh", 0o755); err != nil {
		return err
	}
	if err := os.WriteFile("/etc/ssh/sshd_config", []byte(assets.SSHDConfig), 0o600); err != nil {
		return fmt.Errorf("could not install /etc/ssh/sshd_config: %w", err)
	}
	return EnableService(c, "sshd")
}

// InstallAuthorizedKeys writes root's authorized_keys (if configured).
func InstallAuthorizedKeys(c *Context) error {
	if err := os.MkdirAll("/root/.ssh", 0o700); err != nil {
		return err
	}
	keys := c.Cfg.Packages.RootSSHAuthorizedKeys
	if len(keys) == 0 {
		return nil
	}
	c.R.log("Adding authorized keys for root")
	content := strings.Join(keys, "\n") + "\n"
	if err := os.WriteFile("/root/.ssh/authorized_keys", []byte(content), 0o600); err != nil {
		return fmt.Errorf("could not add ssh key to /root/.ssh/authorized_keys: %w", err)
	}
	return nil
}
