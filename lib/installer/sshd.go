package installer

import (
	"fmt"
	"strings"

	"gentooinstall/assets"
)

// EnableSSHD installs the hardened sshd configuration and enables it.
func EnableSSHD(ctx *Context) error {
	ctx.Runner.log("Installing and enabling sshd")
	if err := ctx.mkdirAll("/etc/ssh", 0o755); err != nil {
		return err
	}
	if err := ctx.writeFile("/etc/ssh/sshd_config", []byte(assets.SSHDConfig), 0o600); err != nil {
		return fmt.Errorf("could not install /etc/ssh/sshd_config: %w", err)
	}
	return EnableService(ctx, "sshd")
}

// InstallAuthorizedKeys writes root's authorized_keys (if configured).
func InstallAuthorizedKeys(ctx *Context) error {
	if err := ctx.mkdirAll("/root/.ssh", 0o700); err != nil {
		return err
	}
	keys := ctx.Cfg.Packages.RootSSHAuthorizedKeys
	if len(keys) == 0 {
		return nil
	}
	ctx.Runner.log("Adding authorized keys for root")
	content := strings.Join(keys, "\n") + "\n"
	if err := ctx.writeFile("/root/.ssh/authorized_keys", []byte(content), 0o600); err != nil {
		return fmt.Errorf("could not add ssh key to /root/.ssh/authorized_keys: %w", err)
	}
	return nil
}
