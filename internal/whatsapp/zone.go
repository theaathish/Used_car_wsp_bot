package whatsapp

// Business timezone for all customer-facing times (confirmations,
// reminders) and for interpreting "today/tomorrow". Live-reloadable from
// admin so no redeploy is needed to change it.

import (
	"sync"
	"time"
)

var (
	zoneMu sync.RWMutex
	zone   = time.FixedZone("IST", 5*3600+1800)
	zoneName = "Asia/Kolkata"
)

// SetZone switches the business timezone (validates via time/tzdata).
func SetZone(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	zoneMu.Lock()
	zone, zoneName = loc, name
	zoneMu.Unlock()
	return loc, nil
}

// Zone returns the current business location.
func Zone() *time.Location {
	zoneMu.RLock()
	defer zoneMu.RUnlock()
	return zone
}

// ZoneName returns the current business timezone name.
func ZoneName() string {
	zoneMu.RLock()
	defer zoneMu.RUnlock()
	return zoneName
}

// FormatTime renders user-facing times like "Sat 12 Sep, 10:00 AM".
func FormatTime(t time.Time) string {
	return t.In(Zone()).Format("Mon 2 Jan, 3:04 PM")
}
