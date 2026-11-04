package installer

import (
	"fmt"
	"path/filepath"
)

func MountRoot(ctx *Context) error {
	if ctx.Layout.Flags.UsedZFS {
		if !IsMountpoint(RootMountpoint) {
			return fmt.Errorf("expected zfs to be mounted under '%s', but it isn't",
				RootMountpoint)
		}
		return nil
	}
	return MountByID(ctx, ctx.Layout.RootID, RootMountpoint)
}

func ClearRoot(ctx *Context) error {
	entries, err := ctx.readDir(RootMountpoint)
	if err != nil {
		return fmt.Errorf("could not read '%s': %w", RootMountpoint, err)
	}
	keep := map[string]bool{
		"lost+found":                    true,
		filepath.Base(Stage3ScratchDir): true,
	}
	for _, entry := range entries {
		if keep[entry.Name()] {
			continue
		}
		if err := ctx.removeAll(RootMountpoint + "/" + entry.Name()); err != nil {
			return fmt.Errorf("could not clear '%s/%s': %w",
				RootMountpoint, entry.Name(), err)
		}
	}
	return nil
}

func ExtractStage3(ctx *Context, stage3 Stage3Info) error {
	if err := MountRoot(ctx); err != nil {
		return err
	}
	if err := MustExist(ctx.path(stage3.Path), "stage3 file"); err != nil {
		return err
	}

	ctx.Runner.log("Extracting stage3 tarball")
	entries, err := ctx.readDir(RootMountpoint)
	if err != nil {
		return fmt.Errorf("could not read '%s': %w", RootMountpoint, err)
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "lost+found", filepath.Base(Stage3ScratchDir):
			continue
		}
		return fmt.Errorf("root directory '%s' is not empty (found %s)",
			RootMountpoint, entry.Name())
	}

	prev := ctx.Runner.Dir
	ctx.Runner.Dir = RootMountpoint
	err = ctx.Runner.Try("tar", "-xpf", stage3.Path, "--xattrs", "--numeric-owner")
	ctx.Runner.Dir = prev
	if err != nil {
		return fmt.Errorf("error while extracting tarball: %w", err)
	}

	if err := ctx.removeAll(Stage3ScratchDir); err != nil {
		ctx.Runner.logf("Warning: could not clean up stage3 scratch dir: %v", err)
	}
	return nil
}
