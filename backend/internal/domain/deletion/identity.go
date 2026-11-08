package deletion

import (
	"encoding/hex"
	"strings"
)

func canonicalUUID(value string) bool {
	if len(value) != 36 || value != strings.ToLower(value) ||
		value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	hexValue := value[:8] + value[9:13] + value[14:18] + value[19:23] + value[24:]
	for _, char := range hexValue {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return false
		}
	}
	decoded, err := hex.DecodeString(hexValue)
	return err == nil && len(decoded) == 16
}

func ValidUUID(value string) bool { return canonicalUUID(value) }
