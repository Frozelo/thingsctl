package thingsdb

import (
	"fmt"
	"time"
)

// Date is a calendar date in the packed form Things stores in startDate,
// deadline and rt1_nextInstanceStartDate: year<<16 | month<<12 | day<<7.
// The zero value means "no date".
type Date int64

// NewDate packs a calendar date.
func NewDate(y int, m time.Month, d int) Date {
	return Date(int64(y)<<16 | int64(m)<<12 | int64(d)<<7)
}

// DateOf returns the calendar date of t in t's location.
func DateOf(t time.Time) Date { return NewDate(t.Year(), t.Month(), t.Day()) }

func (d Date) Year() int         { return int(d >> 16) }
func (d Date) Month() time.Month { return time.Month((d >> 12) & 0xF) }
func (d Date) Day() int          { return int((d >> 7) & 0x1F) }

// IsZero reports whether no date is set.
func (d Date) IsZero() bool { return d == 0 }

// Time returns local midnight of the date.
func (d Date) Time(loc *time.Location) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
}

// AddDays returns the date n days later (n may be negative).
func (d Date) AddDays(n int) Date { return DateOf(d.Time(time.UTC).AddDate(0, 0, n)) }

// ISO formats the date as YYYY-MM-DD.
func (d Date) ISO() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year(), int(d.Month()), d.Day())
}

// Clock is a time of day in the packed form Things stores in reminderTime:
// hour<<26 | minute<<20. The zero value is ambiguous with 00:00, so callers
// must track presence separately (Task.HasReminder).
type Clock int64

// NewClock packs a time of day.
func NewClock(h, m int) Clock { return Clock(int64(h)<<26 | int64(m)<<20) }

func (c Clock) Hour() int   { return int(c >> 26) }
func (c Clock) Minute() int { return int((c >> 20) & 0x3F) }

func (c Clock) String() string { return fmt.Sprintf("%02d:%02d", c.Hour(), c.Minute()) }

// epoch converts a Things REAL unix timestamp to time.Time (zero when unset).
func epoch(sec float64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	s := int64(sec)
	return time.Unix(s, int64((sec-float64(s))*1e9))
}
