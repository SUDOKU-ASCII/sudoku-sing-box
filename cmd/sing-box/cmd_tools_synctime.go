package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"net"
	"os"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/ntp"

	"github.com/spf13/cobra"
)

var (
	commandSyncTimeFlagServer   string
	commandSyncTimeOutputFormat string
	commandSyncTimeWrite        bool
)

var commandSyncTime = &cobra.Command{
	Use:   "synctime",
	Short: "Sync time using the NTP protocol",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		err := syncTime()
		if err != nil {
			log.Fatal(err)
		}
	},
}

func init() {
	commandSyncTime.Flags().StringVarP(&commandSyncTimeFlagServer, "server", "s", "time.apple.com", "Set NTP server")
	commandSyncTime.Flags().StringVarP(&commandSyncTimeOutputFormat, "format", "f", C.TimeLayout, "Set output format")
	commandSyncTime.Flags().BoolVarP(&commandSyncTimeWrite, "write", "w", false, "Write time to system")
	commandTools.AddCommand(commandSyncTime)
}

func syncTime() error {
	instance, err := createPreStartedClient()
	if err != nil {
		return err
	}
	dialer, err := createDialer(instance, commandToolsFlagOutbound)
	if err != nil {
		return err
	}
	defer instance.Close()
	serverAddress := M.ParseSocksaddr(commandSyncTimeFlagServer)
	if serverAddress.Port == 0 {
		serverAddress.Port = 123
	}
	response, err := exchangeNTP(context.Background(), dialer, serverAddress, 5*time.Second)
	if err != nil {
		return err
	}
	if commandSyncTimeWrite {
		err = ntp.SetSystemTime(time.Now().Add(response.ClockOffset))
		if err != nil {
			return E.Cause(err, "write time to system")
		}
	}
	os.Stdout.WriteString(response.Time.Local().Format(commandSyncTimeOutputFormat))
	return nil
}

const ntpEpochOffsetSeconds = 2208988800

func exchangeNTP(ctx context.Context, dialer N.Dialer, serverAddress M.Socksaddr, timeout time.Duration) (*ntp.Response, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pc, err := dialer.ListenPacket(ctx, serverAddress)
	if err != nil {
		return nil, err
	}
	defer pc.Close()
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()

	var addr net.Addr
	if serverAddress.IsIP() {
		addr = serverAddress.UDPAddr()
	} else {
		udpAddr, err := net.ResolveUDPAddr("udp", serverAddress.String())
		if err != nil {
			return nil, E.Cause(err, "resolve udp destination")
		}
		addr = udpAddr
	}

	// Build SNTP request.
	var req [48]byte
	req[0] = 0xE3 // LI=3, VN=4, Mode=3 (client)

	xmitTime := time.Now()
	xmitNTP := timeToNTP(xmitTime)

	// Best effort randomize low bits to avoid fingerprinting.
	var bits [8]byte
	if _, err := rand.Read(bits[:]); err == nil {
		xmitNTP = binary.BigEndian.Uint64(bits[:])
		xmitTime = time.Now()
	}
	binary.BigEndian.PutUint64(req[40:], xmitNTP)

	_, err = pc.WriteTo(req[:], addr)
	if err != nil {
		return nil, err
	}

	var resp [48]byte
	type readResult struct {
		n   int
		err error
	}
	ch := make(chan readResult, 1)
	go func() {
		n, _, err := pc.ReadFrom(resp[:])
		ch <- readResult{n: n, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, E.Cause(os.ErrDeadlineExceeded, "read ntp response")
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		if res.n < len(resp) {
			return nil, E.New("short ntp response: ", res.n)
		}
	}

	recvTime := xmitTime.Add(time.Since(xmitTime))

	originNTP := binary.BigEndian.Uint64(resp[24:32])
	receiveNTP := binary.BigEndian.Uint64(resp[32:40])
	transmitNTP := binary.BigEndian.Uint64(resp[40:48])

	t1 := ntpToTime(originNTP)
	if originNTP == 0 {
		// Some servers don't echo; fall back to local send time.
		t1 = xmitTime
	}
	t2 := ntpToTime(receiveNTP)
	t3 := ntpToTime(transmitNTP)
	t4 := recvTime

	clockOffset := (t2.Sub(t1) + t3.Sub(t4)) / 2
	rtt := (t4.Sub(t1) - t3.Sub(t2))

	return &ntp.Response{
		Time:        t3,
		ClockOffset: clockOffset,
		RTT:         rtt,
	}, nil
}

func timeToNTP(t time.Time) uint64 {
	sec := uint64(t.Unix() + ntpEpochOffsetSeconds)
	frac := uint64(t.Nanosecond()) * (1 << 32) / 1e9
	return sec<<32 | frac
}

func ntpToTime(ntpTime uint64) time.Time {
	sec := int64(ntpTime>>32) - ntpEpochOffsetSeconds
	frac := ntpTime & 0xffffffff
	nsec := int64(frac*1e9) >> 32
	return time.Unix(sec, nsec).UTC()
}
