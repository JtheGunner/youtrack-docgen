package netbox

import (
	"fmt"

	"github.com/JtheGunner/youtrack-docgen/internal/collector/mapbuild"
	"github.com/JtheGunner/youtrack-docgen/internal/model"
)

// keyCounter finds entity keys that more than one NetBox object maps to. Keys
// derive from names, which NetBox does not keep unique (the same device name
// on two sites, a device and a VM with one name, anycast addresses, ...).
type keyCounter map[string]int

func counterKey(kind model.Kind, key string) string {
	return string(kind) + "\x00" + mapbuild.SanitizeKey(key)
}

func (c keyCounter) count(kind model.Kind, key string) { c[counterKey(kind, key)]++ }

// resolve returns key unchanged when a single object claims it. On a
// collision every claimant gets a suffix built from its NetBox ID, so the
// result does not depend on the order of the API responses. Keys without a
// collision stay as they were.
func (c keyCounter) resolve(kind model.Kind, key, family string, id int) string {
	if c[counterKey(kind, key)] < 2 {
		return key
	}
	return fmt.Sprintf("%s~%s%d", key, family, id)
}
