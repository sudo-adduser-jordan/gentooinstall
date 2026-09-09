package installer

import (
	"fmt"
	"path/filepath"
)

// MountRoot mounts the root filesystem at RootMountpoint. For zfs the
// pool must already be mounted there (port of mount_root).
func MountRoot(c *Context) error {
	if c.Layout.Flags.UsedZFS {
		if !IsMountpoint(RootMountpoint) {
			return fmt.Errorf("expected zfs to be mounted under '%s', but it isn't",
				RootMountpoint)
		}
		return nil
	}
	return MountByID(c, c.Layout.RootID, RootMountpoint)
}

// ClearRoot removes everything under the root mountpoint except
// lost+found and the stage3 scratch dir, so a re-attempted extract starts
// from a clean filesystem even after a partially completed previous
// extraction (without discarding the staged, verified tarball).
func ClearRoot(c *Context) error {
	entries, err := c.readDir(RootMountpoint)
	if err != nil {
		return fmt.Errorf("could not read '%s': %w", RootMountpoint, err)
	}
	keep := map[string]bool{
		"lost+found":                    true,
		filepath.Base(Stage3ScratchDir): true,
	}
	for _, e := range entries {
		if keep[e.Name()] {
			continue
		}
		if err := c.removeAll(RootMountpoint + "/" + e.Name()); err != nil {
			return fmt.Errorf("could not clear '%s/%s': %w",
				RootMountpoint, e.Name(), err)
		}
	}
	return nil
}

// ExtractStage3 unpacks the verified tarball into the root mountpoint
// (port of extract_stage3).
func ExtractStage3(c *Context, stage3 Stage3Info) error {
	if err := MountRoot(c); err != nil {
		return err
	}
	if err := MustExist(c.path(stage3.Path), "stage3 file"); err != nil {
		return err
	}

	c.R.log("Extracting stage3 tarball")
	entries, err := c.readDir(RootMountpoint)
	if err != nil {
		return fmt.Errorf("could not read '%s': %w", RootMountpoint, err)
	}
	for _, e := range entries {
		switch e.Name() {
		case "lost+found", filepath.Base(Stage3ScratchDir):
			// lost+found is created by mke2fs; the scratch dir holds the
			// staged tarball we are about to extract.
			continue
		}
		return fmt.Errorf("root directory '%s' is not empty (found %s)",
			RootMountpoint, e.Name())
	}

	prev := c.R.Dir
	c.R.Dir = RootMountpoint
	err = c.R.Try("tar", "-xpf", stage3.Path, "--xattrs", "--numeric-owner")
	c.R.Dir = prev
	if err != nil {
		return fmt.Errorf("error while extracting tarball: %w", err)
	}

	// The staged tarball is no longer needed: removing it frees target-disk
	// space for the chroot work (portage sync, kernel builds) and keeps it
	// out of the installed system.
	if err := c.removeAll(Stage3ScratchDir); err != nil {
		c.R.logf("Warning: could not clean up stage3 scratch dir: %v", err)
	}
	return nil
}
