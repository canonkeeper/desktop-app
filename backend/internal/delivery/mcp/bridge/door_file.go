// SPDX-License-Identifier: AGPL-3.0-only

package bridge

import (
	"encoding/json"
	"errors"
	"io"
	"path/filepath"

	"canonkeeper/backend/internal/delivery/mcp/programtools"
)

// maxDoorFile bounds mcp.json: a path, a fingerprint and a version.
const maxDoorFile = 64 << 10

// readDoor reads mcp.json of the data directory: the file is opened so that the backend may delete or replace it meanwhile,
// and closed at once (MC1.4).
func readDoor(dataDir string) (programtools.DoorFile, error) {
	file, err := openShared(filepath.Join(dataDir, programtools.DoorFileName))
	if err != nil {
		return programtools.DoorFile{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxDoorFile))
	_ = file.Close()
	if err != nil {
		return programtools.DoorFile{}, err
	}
	var door programtools.DoorFile
	if err := json.Unmarshal(raw, &door); err != nil {
		return programtools.DoorFile{}, err
	}
	if door.Socket == "" || !filepath.IsAbs(door.Socket) {
		return programtools.DoorFile{}, errors.New("bridge: mcp.json names no socket")
	}
	return door, nil
}
