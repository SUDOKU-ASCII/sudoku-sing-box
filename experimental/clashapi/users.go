package clashapi

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/ws"
	"github.com/sagernet/ws/wsutil"

	"github.com/go-chi/render"
)

type userJSON struct {
	Name      string `json:"name"`
	Blocked   bool   `json:"blocked"`
	Exceeded  bool   `json:"exceeded"`
	Conns     int    `json:"conns"`
	Up        uint64 `json:"up"`        // bytes per second from the user
	Down      uint64 `json:"down"`      // bytes per second to the user
	UpTotal   uint64 `json:"upTotal"`   // since start
	DownTotal uint64 `json:"downTotal"` // since start
	Used      uint64 `json:"used"`      // this month, counted against limit
	Limit     uint64 `json:"limit"`     // 0 = unlimited
}

type usersJSON struct {
	Count  int        `json:"count"`
	Active int        `json:"active"` // users with an open connection
	Up     uint64     `json:"up"`
	Down   uint64     `json:"down"`
	Users  []userJSON `json:"users"`
}

// usersSampler turns per-user byte counters into per-second rates. One goroutine samples for
// every panel, so the cost does not grow with the number of open streams.
type usersSampler struct {
	inbounds adapter.InboundManager

	mu       sync.Mutex
	previous map[string]adapter.UserStat
	last     time.Time
	latest   usersJSON
}

func newUsersSampler(inbounds adapter.InboundManager) *usersSampler {
	s := &usersSampler{inbounds: inbounds, previous: make(map[string]adapter.UserStat)}
	s.sample(time.Now())
	return s
}

// hasUsers reports whether any inbound currently has a user whitelist; without one the
// sampler is not started at all.
func (s *usersSampler) hasUsers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest.Count > 0
}

func (s *usersSampler) run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case now := <-t.C:
			s.sample(now)
		case <-ctx.Done():
			return
		}
	}
}

func (s *usersSampler) sample(now time.Time) {
	var elapsed float64
	s.mu.Lock()
	previous := s.previous
	if !s.last.IsZero() {
		elapsed = now.Sub(s.last).Seconds()
	}

	type keyedStat struct {
		key  string
		stat adapter.UserStat
	}
	var stats []keyedStat
	if s.inbounds != nil {
		for _, inbound := range s.inbounds.Inbounds() {
			provider, isProvider := inbound.(adapter.UserStatsProvider)
			if !isProvider {
				continue
			}
			for index, stat := range provider.UserStats() {
				// Users keep their config order while the process runs, so the index is stable.
				stats = append(stats, keyedStat{
					key:  inbound.Tag() + "\x00" + strconv.Itoa(index),
					stat: stat,
				})
			}
		}
	}

	users := usersJSON{Count: len(stats), Users: make([]userJSON, len(stats))}
	seen := make(map[string]struct{}, len(stats))
	for i, entry := range stats {
		stat := entry.stat
		user := userJSON{
			Name: stat.Name, Blocked: stat.Blocked, Exceeded: stat.Exceeded, Conns: stat.Conns,
			UpTotal: stat.Up, DownTotal: stat.Down, Used: stat.Used, Limit: stat.Limit,
		}
		if prev, loaded := previous[entry.key]; loaded && elapsed > 0 {
			user.Up = rate(stat.Up, prev.Up, elapsed)
			user.Down = rate(stat.Down, prev.Down, elapsed)
		}
		if user.Conns > 0 {
			users.Active++
		}
		users.Up += user.Up
		users.Down += user.Down
		users.Users[i] = user
		previous[entry.key] = stat
		seen[entry.key] = struct{}{}
	}
	for key := range previous {
		if _, loaded := seen[key]; !loaded {
			delete(previous, key)
		}
	}
	s.latest = users
	s.last = now
	s.mu.Unlock()
}

func rate(current, previous uint64, elapsed float64) uint64 {
	if current < previous {
		return 0
	}
	return uint64(math.Round(float64(current-previous) / elapsed))
}

func (s *usersSampler) snapshot() usersJSON {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest
}

// getUsers reports the users of every inbound with a whitelist, in config order. Over a
// websocket it repeats every second.
func (s *Server) getUsers(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") != "websocket" {
		render.JSON(w, r, s.usersSampler.snapshot())
		return
	}
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		return
	}
	defer conn.Close()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	buf := &bytes.Buffer{}
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		}
		buf.Reset()
		if err = json.NewEncoder(buf).Encode(s.usersSampler.snapshot()); err != nil {
			return
		}
		if err = wsutil.WriteServerText(conn, buf.Bytes()); err != nil {
			return
		}
	}
}
