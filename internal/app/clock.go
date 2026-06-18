package app

import "time"

type Clock interface {
	Now() time.Time
}
