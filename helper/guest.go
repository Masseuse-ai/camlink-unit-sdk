package helper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Masseuse-ai/camlink-unit-sdk/unit"
)

// Options describe the helper to the Host and to the person reading the
// connector's log.
type Options struct {
	// Name is the family's name ("example" for camlink-unit-example).
	Name string
	// Kinds the finder's drivers may report.
	Kinds []unit.Kind
	Log   *slog.Logger
}

// ExitGrace is how long Serve lets requests already running finish once
// stdin ends, before it cancels them: enough for an in-flight
// close(restore=true) to release and unkey the device over a marginal
// link, bounded so a stuck one does not hang the exit.
const ExitGrace = 20 * time.Second

// ExitCloseGrace bounds the close of a device the Host left open without
// closing itself (Serve's own fallback release).
const ExitCloseGrace = 5 * time.Second

// Serve answers a Host on in/out from finder until in ends or ctx is done.
// Requests are served concurrently, each answered on its own line; the
// finder's and the driver's own locking is theirs, as in the connector.
// One device is open at a time: `find` closes a device still open before
// looking again. When stdin ends, requests already running are given
// ExitGrace to finish (an in-flight close especially) before they are
// cancelled.
func Serve(ctx context.Context, in io.Reader, out io.Writer, finder unit.Finder, opts Options) error {
	if opts.Name == "" {
		return errors.New("helper: Serve needs a name")
	}
	g := &guest{finder: finder, opts: opts, out: out, cancels: map[uint64]*atomic.Bool{}}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var wg sync.WaitGroup
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var env Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			g.log().Warn("helper: unreadable request", "err", err)
			continue
		}
		if env.ID == nil {
			g.notification(env)
			continue
		}
		wg.Add(1)
		go func(env Envelope) {
			defer wg.Done()
			g.answer(ctx, env)
		}(env)
	}
	// Stdin ended: the Host is going away. Let requests already running
	// finish first, within a bounded grace — a close(restore=true) the
	// connector sent to release and unkey the device must not be cut off
	// midway, which left the device keyed for the next start to resume —
	// then cancel whatever has not finished.
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(ExitGrace):
		cancel()
		<-finished
	}
	cancel()
	// If the Host went away with a device still open (no close reached
	// us), leave no current flowing behind.
	g.mu.Lock()
	d := g.driver
	g.driver = nil
	g.mu.Unlock()
	if d != nil {
		cctx, ccancel := context.WithTimeout(context.Background(), ExitCloseGrace)
		defer ccancel()
		_ = d.Close(cctx, true)
	}
	return scanner.Err()
}

// Main is what a helper program's main runs: the flags the Host passes
// (--state-dir, --log-level), the log on stderr, then Serve on stdin and
// stdout with the finder built for the state directory. It exits the
// process when the Host goes away.
func Main(build func(stateDir string, log *slog.Logger) (unit.Finder, Options, error)) {
	stateDir := flag.String("state-dir", "", "where the helper keeps what it must remember")
	logLevel := flag.String("log-level", "info", "debug, info, warn or error")
	flag.Parse()
	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	finder, opts, err := build(*stateDir, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if opts.Log == nil {
		opts.Log = log
	}
	if err := Serve(context.Background(), os.Stdin, os.Stdout, finder, opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type guest struct {
	finder unit.Finder
	opts   Options
	out    io.Writer
	outMu  sync.Mutex

	mu     sync.Mutex
	driver unit.Driver
	// cancels are the execute requests running, by id: the Host's cancel
	// flips the flag Execute's cancelled reads.
	cancels map[uint64]*atomic.Bool
}

func (g *guest) log() *slog.Logger {
	if g.opts.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return g.opts.Log
}

func (g *guest) notification(env Envelope) {
	if env.Method != MethodCancel {
		g.log().Debug("helper: notification ignored", "method", env.Method)
		return
	}
	var p CancelParams
	if err := json.Unmarshal(env.Params, &p); err != nil {
		return
	}
	g.mu.Lock()
	flag := g.cancels[p.ID]
	g.mu.Unlock()
	if flag != nil {
		flag.Store(true)
	}
}

func (g *guest) answer(ctx context.Context, env Envelope) {
	result, err := g.dispatch(ctx, env)
	reply := Envelope{ID: env.ID}
	if err != nil {
		reply.Error = ToWire(err)
	} else {
		reply.Result = MustJSON(result)
	}
	line, merr := json.Marshal(reply)
	if merr != nil {
		line, _ = json.Marshal(Envelope{ID: env.ID, Error: &WireError{Code: CodeError, Message: merr.Error()}})
	}
	g.outMu.Lock()
	defer g.outMu.Unlock()
	_, _ = g.out.Write(append(line, '\n'))
}

func (g *guest) current() (unit.Driver, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.driver == nil {
		return nil, &CodedError{Code: CodeNoDriver, Message: "no device is open"}
	}
	return g.driver, nil
}

func (g *guest) dispatch(ctx context.Context, env Envelope) (any, error) {
	switch env.Method {
	case MethodHello:
		return Hello{Protocol: Protocol, Name: g.opts.Name, Kinds: g.opts.Kinds}, nil
	case MethodDescribe:
		var buf strings.Builder
		err := g.finder.Describe(ctx, &buf)
		return DescribeResult{Text: buf.String()}, err
	case MethodList:
		l, ok := g.finder.(unit.Lister)
		if !ok {
			return nil, &CodedError{Code: CodeUnsupported, Message: "this family does not list units"}
		}
		units, err := l.List(ctx)
		if err != nil {
			return nil, err
		}
		if units == nil {
			units = []unit.Unit{}
		}
		return ListResult{Units: units}, nil
	case MethodSelect:
		s, ok := g.finder.(unit.Selector)
		if !ok {
			return nil, &CodedError{Code: CodeUnsupported, Message: "this family does not select units"}
		}
		var p SelectParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return nil, err
		}
		s.Select(p.Unit)
		return struct{}{}, nil
	case MethodFind:
		g.mu.Lock()
		previous := g.driver
		g.driver = nil
		g.mu.Unlock()
		if previous != nil {
			_ = previous.Close(ctx, true)
		}
		d, err := g.finder.Find(ctx)
		if err != nil {
			return nil, err
		}
		g.mu.Lock()
		g.driver = d
		g.mu.Unlock()
		found := Found{Kind: d.Kind(), Label: d.Label(), Identity: unit.IdentityOf(d), Port: d.Port(), Capabilities: d.Capabilities()}
		if h, ok := d.(unit.HeldReporter); ok {
			found.Held = h.Held()
		}
		if _, ok := d.(unit.ArmRenewer); ok {
			found.RenewsArm = true
		}
		found.Actuators = unit.ActuatorsOf(d)
		found.Sensors = unit.SensorsOf(d)
		return found, nil
	case MethodRelease:
		d, err := g.current()
		if err != nil {
			return nil, err
		}
		return struct{}{}, d.Release(ctx)
	case MethodArm:
		d, err := g.current()
		if err != nil {
			return nil, err
		}
		var p ArmParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return nil, err
		}
		return struct{}{}, d.Arm(ctx, p.PowerMode)
	case MethodRenewArm:
		d, err := g.current()
		if err != nil {
			return nil, err
		}
		renewer, ok := d.(unit.ArmRenewer)
		if !ok {
			return struct{}{}, nil
		}
		var p RenewArmParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return nil, err
		}
		until, err := time.Parse(time.RFC3339Nano, p.Until)
		if err != nil {
			return nil, fmt.Errorf("helper: renewArm: %w", err)
		}
		return struct{}{}, renewer.RenewArm(ctx, until)
	case MethodStatus:
		d, err := g.current()
		if err != nil {
			return nil, err
		}
		st, err := d.Status(ctx)
		if err != nil {
			return nil, err
		}
		return StatusResult{Status: st}, nil
	case MethodTelemetry:
		d, err := g.current()
		if err != nil {
			return nil, err
		}
		f, err := d.Telemetry(ctx)
		if err != nil {
			return nil, err
		}
		return TelemetryResult{Frame: f}, nil
	case MethodExecute:
		d, err := g.current()
		if err != nil {
			return nil, err
		}
		var p ExecuteParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return nil, err
		}
		flag := &atomic.Bool{}
		g.mu.Lock()
		g.cancels[*env.ID] = flag
		g.mu.Unlock()
		defer func() {
			g.mu.Lock()
			delete(g.cancels, *env.ID)
			g.mu.Unlock()
		}()
		res, err := d.Execute(ctx, p.Command, p.LevelMax, flag.Load)
		if err != nil {
			return nil, err
		}
		return ExecuteResult{Result: res}, nil
	case MethodActuate:
		d, err := g.current()
		if err != nil {
			return nil, err
		}
		a, ok := d.(unit.Actuating)
		if !ok {
			return nil, &CodedError{Code: CodeUnsupported, Message: "this device has no actuators"}
		}
		var p ActuateParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return nil, err
		}
		// The Host checked the command against the actuators it was given;
		// the helper checks again, against what the driver says now, so a
		// helper is safe with any Host.
		if err := unit.CheckActuate(p.Command, a.Actuators(), 100); err != nil {
			return nil, err
		}
		return struct{}{}, a.Actuate(ctx, p.Command)
	case MethodReadings:
		d, err := g.current()
		if err != nil {
			return nil, err
		}
		s, ok := d.(unit.Sensing)
		if !ok {
			return nil, &CodedError{Code: CodeUnsupported, Message: "this device has no sensors"}
		}
		rs, err := s.Readings(ctx)
		if err != nil {
			return nil, err
		}
		if rs == nil {
			rs = []unit.Reading{}
		}
		return ReadingsResult{Readings: rs}, nil
	case MethodClose:
		g.mu.Lock()
		d := g.driver
		g.driver = nil
		g.mu.Unlock()
		if d == nil {
			return struct{}{}, nil
		}
		var p CloseParams
		if len(env.Params) > 0 {
			if err := json.Unmarshal(env.Params, &p); err != nil {
				return nil, err
			}
		}
		return struct{}{}, d.Close(ctx, p.Restore)
	}
	return nil, &CodedError{Code: CodeUnsupported, Message: fmt.Sprintf("unknown method %q", env.Method)}
}
