// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import "database/sql/driver"

type AsyncJobStatus string

func (s AsyncJobStatus) Value() (driver.Value, error) {
	return string(s), nil
}
