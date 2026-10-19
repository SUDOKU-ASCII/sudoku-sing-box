/*
Copyright (C) 2026 by saba <contact me via issue>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
*/

package users

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/logx"
	"github.com/sagernet/sing/service/filemanager"
)

// Data quotas apply per calendar month in server local time.
func usageMonth(t time.Time) string { return t.Format("2006-01") }

// usageFile maps each user's configured hash (or hash prefix) to the wire bytes used in Month.
// Keying by hash keeps usage across alias edits and resets it when the key changes.
type usageFile struct {
	Month string            `json:"month"`
	Used  map[string]uint64 `json:"used"`
}

// TrackUsage restores this month's usage from path, saves it every minute and once more on
// stop, and resets every user when a new month begins. An empty path keeps usage in memory.
func (r *Registry) TrackUsage(ctx context.Context, path string) (stop func(), err error) {
	if r == nil {
		return func() {}, nil
	}
	month := usageMonth(time.Now())
	if err = r.loadUsage(ctx, path, month); err != nil {
		return nil, err
	}
	save := func() bool {
		err := r.saveUsage(ctx, path, month)
		if err != nil {
			logx.Warnf("Server", "save user usage: %v", err)
		}
		return err == nil
	}

	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		saved := r.totalUsed()
		for {
			select {
			case <-t.C:
			case <-done:
				return
			}
			if m := usageMonth(time.Now()); m != month {
				month = m
				r.resetUsage()
				logx.Infof("Server", "New month %s, user data usage reset", month)
			}
			// Within a month usage only grows, so an unchanged total means nothing to write.
			if total := r.totalUsed(); total != saved && save() {
				saved = total
			}
		}
	}()
	return func() {
		close(done)
		<-exited
		save()
	}, nil
}

// loadUsage ignores a missing file and usage saved in an earlier month.
func (r *Registry) loadUsage(ctx context.Context, path, month string) error {
	if path == "" {
		return nil
	}
	b, err := filemanager.ReadFile(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f usageFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Month == month {
		r.each(func(u *User) { u.used.Store(f.Used[u.prefix]) })
	}
	return nil
}

// saveUsage writes atomically, so a crash mid-write keeps the previous file.
// Users no longer in the config are dropped.
func (r *Registry) saveUsage(ctx context.Context, path, month string) error {
	if path == "" {
		return nil
	}
	f := usageFile{Month: month, Used: make(map[string]uint64)}
	r.each(func(u *User) {
		if n := u.used.Load(); n > 0 {
			f.Used[u.prefix] = n
		}
	})
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := filemanager.WriteFile(ctx, tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return filemanager.Rename(ctx, tmp, path)
}

func (r *Registry) resetUsage() {
	r.each(func(u *User) {
		u.used.Store(0)
		u.kicked.Store(false)
	})
}

func (r *Registry) totalUsed() (total uint64) {
	r.each(func(u *User) { total += u.used.Load() })
	return
}

func (r *Registry) each(fn func(*User)) {
	for _, u := range r.all {
		fn(u)
	}
}
