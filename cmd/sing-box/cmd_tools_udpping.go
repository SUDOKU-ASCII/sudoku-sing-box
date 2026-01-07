package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/spf13/cobra"
)

var (
	commandUDPPingFlagCount    int
	commandUDPPingFlagInterval time.Duration
	commandUDPPingFlagTimeout  time.Duration
	commandUDPPingFlagPayload  string
)

var commandUDPPing = &cobra.Command{
	Use:   "udpping <address>",
	Short: "Send UDP ping packets",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		err := udpPing(args[0])
		if err != nil {
			log.Fatal(err)
		}
	},
}

func init() {
	commandUDPPing.Flags().IntVarP(&commandUDPPingFlagCount, "count", "c", 4, "Number of packets to send")
	commandUDPPing.Flags().DurationVarP(&commandUDPPingFlagInterval, "interval", "i", time.Second, "Interval between packets")
	commandUDPPing.Flags().DurationVarP(&commandUDPPingFlagTimeout, "timeout", "t", 2*time.Second, "Timeout for each packet")
	commandUDPPing.Flags().StringVarP(&commandUDPPingFlagPayload, "payload", "p", "ping", "Payload to send")
	commandTools.AddCommand(commandUDPPing)
}

func udpPing(address string) error {
	if commandUDPPingFlagCount <= 0 {
		return E.New("invalid count: ", commandUDPPingFlagCount)
	}
	if commandUDPPingFlagTimeout <= 0 {
		return E.New("invalid timeout: ", commandUDPPingFlagTimeout)
	}
	if commandUDPPingFlagInterval < 0 {
		return E.New("invalid interval: ", commandUDPPingFlagInterval)
	}

	destination := M.ParseSocksaddr(address)
	if !destination.IsValid() || destination.Port == 0 {
		return E.New("invalid destination: ", address)
	}

	var targetAddr net.Addr
	if destination.IsIP() {
		targetAddr = destination.UDPAddr()
	} else {
		addr, err := net.ResolveUDPAddr("udp", destination.String())
		if err != nil {
			return E.Cause(err, "resolve destination")
		}
		targetAddr = addr
	}

	instance, err := createPreStartedClient()
	if err != nil {
		return err
	}
	defer instance.Close()

	dialer, err := createDialer(instance, commandToolsFlagOutbound)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pc, err := dialer.ListenPacket(ctx, destination)
	if err != nil {
		return E.Cause(err, "listen packet")
	}
	defer pc.Close()
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()

	buf := make([]byte, 64*1024)
	for i := 0; i < commandUDPPingFlagCount; i++ {
		payload := []byte(commandUDPPingFlagPayload)
		if commandUDPPingFlagCount > 1 {
			payload = []byte(fmt.Sprintf("%s %d", commandUDPPingFlagPayload, i+1))
		}

		start := time.Now()
		_, err := pc.WriteTo(payload, targetAddr)
		if err != nil {
			return E.Cause(err, "send udp packet")
		}

		type readResult struct {
			n    int
			from net.Addr
			err  error
		}
		ch := make(chan readResult, 1)
		go func() {
			n, from, err := pc.ReadFrom(buf)
			ch <- readResult{n: n, from: from, err: err}
		}()
		var n int
		var from net.Addr
		select {
		case res := <-ch:
			if res.err != nil {
				return E.Cause(res.err, "read udp packet")
			}
			n = res.n
			from = res.from
		case <-time.After(commandUDPPingFlagTimeout):
			cancel()
			return E.Cause(os.ErrDeadlineExceeded, "read udp packet")
		}

		_, _ = fmt.Fprintf(os.Stdout, "reply from %s: bytes=%d time=%s\n", from.String(), n, time.Since(start).Round(time.Millisecond))
		if commandUDPPingFlagInterval > 0 && i+1 < commandUDPPingFlagCount {
			time.Sleep(commandUDPPingFlagInterval)
		}
	}
	return nil
}
