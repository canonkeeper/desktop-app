// SPDX-License-Identifier: AGPL-3.0-only

//go:build !windows

package main

import "errors"

func localAppData() (string, error) { return "", errors.New("Windows only") }

func shortPath(path string) (string, error) { return path, nil }

func activeCodePage() uint32 { return 0 }
