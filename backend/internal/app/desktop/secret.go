// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

// clusterSecrets are the SCRAM passwords of the roles of the cluster (DA1.3, DA3.10), made at the first start and kept
// in pg.secret, readable by the user alone. They never reach the arguments or the environment of a process.
type clusterSecrets struct {
	Admin   string `json:"admin"`
	Product string `json:"product"`
	Worker  string `json:"worker"`
}

var errClusterSecrets = errors.New("desktop: the passwords of the cluster are unreadable")

func (secrets clusterSecrets) valid() bool {
	return len(secrets.Admin) == 64 && len(secrets.Product) == 64 && len(secrets.Worker) == 64
}

// loadClusterSecrets reads pg.secret, or makes it when there is none.
func loadClusterSecrets(path string) (clusterSecrets, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		secrets := clusterSecrets{Admin: randomHex(), Product: randomHex(), Worker: randomHex()}
		encoded, _ := json.Marshal(secrets)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return clusterSecrets{}, err
		}
		if _, err := file.Write(encoded); err != nil {
			_ = file.Close()
			return clusterSecrets{}, err
		}
		return secrets, file.Close()
	}
	if err != nil {
		return clusterSecrets{}, err
	}
	var secrets clusterSecrets
	if json.Unmarshal(raw, &secrets) != nil || !secrets.valid() {
		return clusterSecrets{}, errClusterSecrets
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return clusterSecrets{}, err
	}
	return secrets, nil
}

// randomHex is 32 random bytes in hex.
func randomHex() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}
