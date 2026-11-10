// cage-mobile-link provides a host bridge and an on-device browser client for
// testing pre-provisioned mobile links. CLI state is explicitly selected.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sylvinus/agent-vm/internal/mobilelink"
)

const usage = `cage-mobile-link init --state NEW_DIRECTORY
cage-mobile-link fingerprint --state DIRECTORY
cage-mobile-link trust --state DIRECTORY --peer-pin VERIFIED_PUBLIC_FINGERPRINT
cage-mobile-link revoke --state DIRECTORY
cage-mobile-link host --state DIRECTORY [--relay HTTPS_ORIGIN] [--admin-token-file FILE] --invitation-out NEW_FILE --cage-bin ABSOLUTE_BINARY --config ABSOLUTE_CONFIG
cage-mobile-link host --state DIRECTORY [--relay HTTPS_ORIGIN] [--admin-token-file ABSOLUTE_FILE] --connection-out NEW_FILE --cage-bin ABSOLUTE_BINARY --config ABSOLUTE_CONFIG
cage-mobile-link host --state DIRECTORY --serve-saved
cage-mobile-link client --state DIRECTORY [--relay HTTPS_ORIGIN] --invitation-file FILE --web-dir BUILD_DIRECTORY --launch-file NEW_HTML_FILE [--listen 127.0.0.1:0]
cage-mobile-link client --state DIRECTORY [--relay HTTPS_ORIGIN] --connection-file FILE --web-dir BUILD_DIRECTORY --launch-file NEW_HTML_FILE [--listen 127.0.0.1:0]
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil && !errors.Is(err, flag.ErrHelp) && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "cage-mobile-link:", err)
		os.Exit(1)
	}
}

// New pairings use the desktop preference. Existing pairings retain the
// origin to which their lookup and device credentials were issued.
func profileRelay(config string) (string, error) {
	var prefs struct {
		MobileRelay string `json:"mobile_relay"`
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(config), "workbench.json"))
	if errors.Is(err, os.ErrNotExist) {
		return mobilelink.OfficialRelay, nil
	}
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(b, &prefs); err != nil {
		return "", err
	}
	if prefs.MobileRelay == "" {
		return mobilelink.OfficialRelay, nil
	}
	if !strings.HasPrefix(prefs.MobileRelay, "https://") || !mobilelink.ValidRelayOrigin(prefs.MobileRelay) {
		return "", errors.New("invalid mobile relay in Cage settings")
	}
	return prefs.MobileRelay, nil
}

type clientInvitation struct {
	ID        string    `json:"id"`
	Token     string    `json:"client_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func run(ctx context.Context, args []string, out, errs io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		_, _ = io.WriteString(out, usage)
		return nil
	}
	cmd := args[0]
	f := flag.NewFlagSet("cage-mobile-link "+cmd, flag.ContinueOnError)
	f.SetOutput(errs)
	var state, pin, base, adminFile, invitation, connectionFile, cageBinary, config, webDir, launchFile, address string
	var serveSaved bool
	f.StringVar(&state, "state", "", "private device state directory")
	switch cmd {
	case "init", "fingerprint", "revoke":
	case "trust":
		f.StringVar(&pin, "peer-pin", "", "peer fingerprint verified through a trusted channel")
	case "host":
		f.StringVar(&base, "relay", "", "trusted relay HTTPS origin")
		f.StringVar(&adminFile, "admin-token-file", "", "private relay admin token file")
		f.StringVar(&invitation, "invitation-out", "", "new private file for client room credentials")
		f.StringVar(&connectionFile, "connection-out", "", "new private file for the paired phone to save")
		f.BoolVar(&serveSaved, "serve-saved", false, "keep serving the existing saved device pairing")
		f.StringVar(&cageBinary, "cage-bin", "", "absolute path to trusted cage binary")
		f.StringVar(&config, "config", "", "absolute path to the running Cage profile configuration")
	case "client":
		f.StringVar(&base, "relay", mobilelink.OfficialRelay, "trusted relay HTTPS origin")
		f.StringVar(&invitation, "invitation-file", "", "private invitation supplied by the host")
		f.StringVar(&connectionFile, "connection-file", "", "private saved connection file verified on this device")
		f.StringVar(&webDir, "web-dir", "", "trusted build directory containing mobile.html")
		f.StringVar(&launchFile, "launch-file", "", "new private local HTML entry file")
		f.StringVar(&address, "listen", "127.0.0.1:0", "local numeric loopback UI listener")
	default:
		return errors.New("unknown command; use --help")
	}
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if state == "" || f.NArg() != 0 {
		return errors.New("--state is required; positional arguments are not supported")
	}
	if cmd == "init" {
		id, err := createIdentity(state)
		if err == nil {
			fmt.Fprintln(out, id.Pin)
		}
		return err
	}
	if cmd == "trust" {
		return trust(state, pin)
	}
	if cmd == "revoke" {
		return revoke(state)
	}
	id, err := loadIdentity(state)
	if err != nil {
		return err
	}
	if cmd == "fingerprint" {
		fmt.Fprintln(out, id.Pin)
		return nil
	}
	peer, err := trustedPeer(state)
	if err != nil {
		return err
	}
	if cmd == "host" {
		if serveSaved {
			if base != "" || adminFile != "" || invitation != "" || connectionFile != "" || cageBinary != "" || config != "" {
				return errors.New("--serve-saved uses only the configuration saved for --state")
			}
			h, err := loadSavedHost(state, id.Pin, peer)
			if err != nil {
				return err
			}
			return savedService(ctx, state, h, errs)
		}
		if base == "" {
			if adminFile != "" {
				return errors.New("specify --relay together with --admin-token-file to confirm the destination of the private token")
			}
			if !filepath.IsAbs(config) {
				return errors.New("host requires an absolute --config")
			}
			base, err = profileRelay(config)
			if err != nil {
				return err
			}
		}
		if connectionFile != "" {
			if invitation != "" {
				return errors.New("choose --connection-out or --invitation-out")
			}
			h, err := configureSavedHost(state, connectionFile, id.Pin, peer, hostSettings{Relay: base, AdminFile: adminFile, CageBinary: cageBinary, Config: config})
			if err != nil {
				return err
			}
			fmt.Fprintln(out, "Private saved connection file written. Verify the host fingerprint separately on the phone.")
			return savedService(ctx, state, h, errs)
		}
		if !filepath.IsAbs(cageBinary) || !filepath.IsAbs(config) || invitation == "" {
			return errors.New("host requires absolute --cage-bin, --config, and a new --invitation-out file")
		}
		b, err := localWorkbench(ctx, cageBinary, config)
		if err != nil {
			return err
		}
		defer b.Close()
		admin, err := readAdminToken(adminFile)
		if err != nil {
			return err
		}
		room, err := mobilelink.CreateRoom(ctx, base, admin)
		if err != nil {
			return err
		}
		data, _ := json.Marshal(clientInvitation{room.ID, room.ClientToken, room.ExpiresAt})
		err = writeNewPrivate(invitation, data)
		clear(data)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "Private client invitation saved. Waiting for the trusted device.")
		active, stop := monitorTrust(ctx, func() bool { current, err := trustedPeer(state); return err == nil && current == peer })
		defer stop()
		stopAwake := keepHostAwake(active, log.New(errs, "", 0))
		defer stopAwake()
		err = mobilelink.ServeHost(active, base, room.ID, room.HostToken, id, peer, b)
		if active.Err() != nil {
			return context.Cause(active)
		}
		return err
	}
	if launchFile == "" || webDir == "" {
		return errors.New("client requires --web-dir and a new --launch-file")
	}
	var room clientInvitation
	if connectionFile != "" {
		if invitation != "" {
			return errors.New("choose --connection-file or --invitation-file")
		}
		room, err = resolveSaved(ctx, connectionFile, base, peer)
		if err != nil {
			return err
		}
	} else {
		data, err := readPrivate(invitation, 4096)
		if err != nil {
			return err
		}
		err = json.Unmarshal(data, &room)
		clear(data)
		if err != nil || !room.ExpiresAt.After(time.Now()) {
			return errors.New("invalid or expired client invitation")
		}
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("the local browser must use a numeric loopback address")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return errors.New("cannot listen on the requested loopback address")
	}
	defer listener.Close()
	client, err := mobilelink.ConnectClient(ctx, base, room.ID, room.Token, id, peer)
	if err != nil {
		return err
	}
	defer client.Close()
	browser, entry, err := mobilelink.NewBrowser("http://"+listener.Addr().String(), webDir, client)
	if err != nil {
		return err
	}
	defer browser.Close()
	page := []byte(`<!doctype html><meta charset="utf-8"><meta name="referrer" content="no-referrer"><meta http-equiv="refresh" content="0;url=` + html.EscapeString(entry) + `"><title>Cage</title>`)
	err = writeNewPrivate(launchFile, page)
	clear(page)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Authenticated. Open the private HTML launch file on this device within two minutes.")
	server := &http.Server{Handler: browser, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	stop := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stop()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type entryBuffer struct{ buf bytes.Buffer }

func (b *entryBuffer) Write(p []byte) (int, error) {
	if len(p) > (16<<10)-b.buf.Len() {
		return 0, errors.New("Cage entry too large")
	}
	return b.buf.Write(p)
}

func localWorkbench(ctx context.Context, cageBinary, config string) (*mobilelink.Workbench, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cageBinary, "server-entry", "--json")
	cmd.Env = append(os.Environ(), "CLAUDE_CAGE_CONFIG="+config)
	buf := &entryBuffer{}
	cmd.Stdout, cmd.Stderr = buf, io.Discard
	if err := cmd.Run(); err != nil {
		return nil, errors.New("Cage could not issue a local entry; start the selected profile first")
	}
	defer clear(buf.buf.Bytes())
	var entry struct{ Origin, Ticket string }
	if json.Unmarshal(buf.buf.Bytes(), &entry) != nil {
		return nil, errors.New("Cage returned an invalid local entry")
	}
	return mobilelink.OpenWorkbench(ctx, entry.Origin, entry.Ticket)
}
