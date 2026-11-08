package codex

import (
	"errors"
	"os"
	"path/filepath"
)

func PrepareHome(runtimeHome, repositoryHome string) error {
	if !filepath.IsAbs(runtimeHome) || !filepath.IsAbs(repositoryHome) || runtimeHome == repositoryHome {
		return ErrInvalid
	}
	if info, err := os.Stat(repositoryHome); err != nil || !info.IsDir() {
		return errors.Join(ErrInvalid, err)
	}
	if err := os.MkdirAll(runtimeHome, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"config.toml", "model-catalog.json", "AGENTS.md", "agents", "skills"} {
		target := filepath.Join(repositoryHome, name)
		link := filepath.Join(runtimeHome, name)
		if _, err := os.Stat(target); err != nil {
			if name == "agents" && errors.Is(err, os.ErrNotExist) {
				if _, linkErr := os.Lstat(link); errors.Is(linkErr, os.ErrNotExist) {
					continue
				} else if linkErr == nil {
					return ErrConflict
				} else {
					return linkErr
				}
			}
			return err
		}
		current, err := os.Readlink(link)
		switch {
		case err == nil && current == target:
			continue
		case err == nil:
			return ErrConflict
		case !errors.Is(err, os.ErrNotExist):
			if _, statErr := os.Lstat(link); statErr == nil {
				return ErrConflict
			}
			return err
		}
		if err := os.Symlink(target, link); err != nil {
			return err
		}
	}
	return nil
}
