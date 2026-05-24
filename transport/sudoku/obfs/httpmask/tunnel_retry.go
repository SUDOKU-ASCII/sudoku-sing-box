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
package httpmask

import (
	"errors"
	"io"
	"net"
	"net/url"
	"time"
)

func isDialError(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return isDialError(urlErr.Err)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Op == "dial" || opErr.Op == "connect" {
			return true
		}
	}
	return false
}

func resetTimer(t *time.Timer, d time.Duration) {
	if t == nil {
		return
	}
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

func retryDial(closed <-chan struct{}, closedErr func() error, maxRetry int, minBackoff, maxBackoff time.Duration, fn func() error) error {
	backoff := minBackoff
	for tries := 0; ; tries++ {
		if err := fn(); err == nil {
			return nil
		} else if isDialError(err) && tries < maxRetry {
			select {
			case <-time.After(backoff):
			case <-closed:
				if closedErr != nil {
					return closedErr()
				}
				return io.ErrClosedPipe
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		} else {
			return err
		}
	}
}
