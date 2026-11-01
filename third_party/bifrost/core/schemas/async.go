package schemas

import "database/sql/driver"

type AsyncJobStatus string

func (s AsyncJobStatus) Value() (driver.Value, error) {
	return string(s), nil
}
