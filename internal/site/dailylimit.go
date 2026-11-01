package site

import (
	"sync"
	"time"
	_ "time/tzdata" // Asia/Beirut on images without a zone database
)

// Daily AI limits reset at midnight in Beirut, where the readers are.
var beirut = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Beirut"); err == nil {
		return l
	}
	return time.FixedZone("EET", 2*3600)
}()

const (
	questionsPerDay = 10  // per device (ll_ai cookie)
	banPerDay       = 100 // questions from one IP in one day that get it banned
	banFor          = 30 * 24 * time.Hour
)

func beirutDay(t time.Time) string { return t.In(beirut).Format("2006-01-02") }

// untilMidnight is how long until the next day starts in Beirut.
func untilMidnight(now time.Time) time.Duration {
	t := now.In(beirut)
	next := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, beirut)
	return next.Sub(t)
}

// dailyCounter counts per key and forgets everything when the Beirut day
// changes, so it never grows past one day of keys.
type dailyCounter struct {
	mu  sync.Mutex
	day string
	n   map[string]int
}

func (c *dailyCounter) roll(now time.Time) {
	if d := beirutDay(now); d != c.day || c.n == nil {
		c.day, c.n = d, map[string]int{}
	}
}

// take counts one more for key unless it already reached limit (0: no
// limit). It returns the count after the call and whether it was taken.
func (c *dailyCounter) take(key string, limit int) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.roll(time.Now())
	if limit > 0 && c.n[key] >= limit {
		return c.n[key], false
	}
	c.n[key]++
	return c.n[key], true
}

// banList holds IPs barred from AI answers until a time. It lives in the
// server's memory: a restart (a deploy) lifts every ban.
type banList struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func (b *banList) banned(ip string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.until[ip]
	if ok && time.Now().After(t) {
		delete(b.until, ip)
		return false
	}
	return ok
}

func (b *banList) ban(ip string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.until == nil {
		b.until = map[string]time.Time{}
	}
	b.until[ip] = time.Now().Add(banFor)
}
