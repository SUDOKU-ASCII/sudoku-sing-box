package main

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/spf13/cobra"
)

var (
	commandDNSQueryFlagType    string
	commandDNSQueryFlagTimeout time.Duration
)

var commandDNSQuery = &cobra.Command{
	Use:   "dnsquery <server> <name>",
	Short: "Send a DNS query over UDP",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		err := dnsQuery(args[0], args[1])
		if err != nil {
			log.Fatal(err)
		}
	},
}

func init() {
	commandDNSQuery.Flags().StringVarP(&commandDNSQueryFlagType, "type", "t", "A", "Query type: A/AAAA/TXT")
	commandDNSQuery.Flags().DurationVar(&commandDNSQueryFlagTimeout, "timeout", 5*time.Second, "Timeout for the query")
	commandTools.AddCommand(commandDNSQuery)
}

func dnsQuery(server string, name string) error {
	if commandDNSQueryFlagTimeout <= 0 {
		return E.New("invalid timeout: ", commandDNSQueryFlagTimeout)
	}

	serverAddress := M.ParseSocksaddr(server)
	if !serverAddress.IsValid() {
		return E.New("invalid server: ", server)
	}
	if serverAddress.Port == 0 {
		serverAddress.Port = 53
	}

	questionName := strings.TrimSpace(name)
	if questionName == "" {
		return E.New("empty name")
	}
	questionName = strings.TrimSuffix(questionName, ".")

	qtype, err := dnsType(commandDNSQueryFlagType)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandDNSQueryFlagTimeout)
	defer cancel()

	instance, err := createPreStartedClient()
	if err != nil {
		return err
	}
	defer instance.Close()

	dialer, err := createDialer(instance, commandToolsFlagOutbound)
	if err != nil {
		return err
	}

	pc, err := dialer.ListenPacket(ctx, serverAddress)
	if err != nil {
		return E.Cause(err, "listen packet")
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
			return E.Cause(err, "resolve udp destination")
		}
		addr = udpAddr
	}

	id := randomUint16()
	req, err := buildDNSQuery(id, questionName, qtype)
	if err != nil {
		return err
	}

	start := time.Now()
	_, err = pc.WriteTo(req, addr)
	if err != nil {
		return E.Cause(err, "send dns query")
	}

	buf := make([]byte, 4096)
	type readResult struct {
		n   int
		err error
	}
	ch := make(chan readResult, 1)
	go func() {
		n, _, err := pc.ReadFrom(buf)
		ch <- readResult{n: n, err: err}
	}()

	select {
	case <-ctx.Done():
		return E.Cause(os.ErrDeadlineExceeded, "read dns response")
	case res := <-ch:
		if res.err != nil {
			return E.Cause(res.err, "read dns response")
		}
		if res.n < 12 {
			return E.New("short dns response: ", res.n)
		}
		respID := binary.BigEndian.Uint16(buf[:2])
		if respID != id {
			return E.New("dns response id mismatch: ", respID, " != ", id)
		}
		rcode := buf[3] & 0x0f
		if rcode != 0 {
			return E.New("dns error rcode=", rcode)
		}
		ancount := binary.BigEndian.Uint16(buf[6:8])
		_, _ = fmt.Fprintf(os.Stdout, "ok rtt=%s answers=%d\n", time.Since(start).Round(time.Millisecond), ancount)
		return nil
	}
}

func dnsType(s string) (uint16, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "A":
		return 1, nil
	case "AAAA":
		return 28, nil
	case "TXT":
		return 16, nil
	default:
		return 0, E.New("unsupported type: ", s)
	}
}

func buildDNSQuery(id uint16, name string, qtype uint16) ([]byte, error) {
	labels := strings.Split(name, ".")
	for _, label := range labels {
		if label == "" {
			return nil, E.New("invalid name: ", name)
		}
		if len(label) > 63 {
			return nil, E.New("label too long: ", label)
		}
	}

	// Header (12 bytes) + QNAME + QTYPE/QCLASS.
	qnameLen := 1 // trailing zero
	for _, label := range labels {
		qnameLen += 1 + len(label)
	}
	msg := make([]byte, 12+qnameLen+4)

	binary.BigEndian.PutUint16(msg[0:2], id)
	binary.BigEndian.PutUint16(msg[2:4], 0x0100) // RD=1
	binary.BigEndian.PutUint16(msg[4:6], 1)      // QDCOUNT

	pos := 12
	for _, label := range labels {
		msg[pos] = byte(len(label))
		pos++
		copy(msg[pos:], label)
		pos += len(label)
	}
	msg[pos] = 0
	pos++

	binary.BigEndian.PutUint16(msg[pos:pos+2], qtype)
	binary.BigEndian.PutUint16(msg[pos+2:pos+4], 1) // IN
	return msg, nil
}

func randomUint16() uint16 {
	var b [2]byte
	if _, err := crand.Read(b[:]); err != nil {
		return uint16(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint16(b[:])
}

