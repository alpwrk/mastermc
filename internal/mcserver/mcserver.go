// Package mcserver starts and supervises the Minecraft server process.
package mcserver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"mastermc/internal/config"
	"mastermc/internal/events"
)

type State string

const (
	Stopped  State = "stopped"
	Starting State = "starting"
	Running  State = "running"
	Stopping State = "stopping"
)

const maxLogLines = 2000

type LogLine struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

type Status struct {
	State     State   `json:"state"`
	PID       int     `json:"pid"`
	StartedAt int64   `json:"startedAt"` // Unix seconds, 0 when stopped
	CPU       float64 `json:"cpu"`       // percent (of one core)
	MemoryMB  float64 `json:"memoryMb"`
	Java      string  `json:"java"`
}

type Server struct {
	cfg         *config.Store
	hub         *events.Hub
	resolveJava func(ctx context.Context) (string, error)

	mu        sync.Mutex
	state     State
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	done      chan struct{}
	startedAt time.Time
	javaPath  string
	wantStop  bool
	restartIn bool

	logMu  sync.Mutex
	logs   []LogLine
	nextID int64

	cpuMu     sync.Mutex
	lastTicks uint64
	lastCPUAt time.Time
	lastCPU   float64
}

func New(cfg *config.Store, hub *events.Hub, resolveJava func(ctx context.Context) (string, error)) *Server {
	return &Server{cfg: cfg, hub: hub, resolveJava: resolveJava, state: Stopped}
}

func (s *Server) setState(st State) {
	s.state = st
	s.hub.Publish("state", st)
}

// Log appends a line to the console log (also used for panel messages).
func (s *Server) Log(text string) {
	s.logMu.Lock()
	s.nextID++
	l := LogLine{ID: s.nextID, Text: text}
	s.logs = append(s.logs, l)
	if len(s.logs) > maxLogLines {
		s.logs = append([]LogLine(nil), s.logs[len(s.logs)-maxLogLines:]...)
	}
	s.logMu.Unlock()
	s.hub.Publish("log", l)
}

func (s *Server) Logs() []LogLine {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	return append([]LogLine(nil), s.logs...)
}

func (s *Server) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Server) Start() error {
	s.mu.Lock()
	if s.state != Stopped {
		s.mu.Unlock()
		return errors.New("server is already running")
	}
	cfg := s.cfg.Get()
	dir := s.cfg.ServerDir()
	jar := filepath.Join(dir, cfg.JarName)
	if _, err := os.Stat(jar); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("%s not found – please install a server first", cfg.JarName)
	}
	s.setState(Starting)
	s.mu.Unlock()

	// Resolve Java (may trigger a download) without holding the mutex.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	javaPath, err := s.resolveJava(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.setState(Stopped)
		return fmt.Errorf("Java: %w", err)
	}

	args := []string{}
	if cfg.MinRAM != "" {
		args = append(args, "-Xms"+cfg.MinRAM)
	}
	if cfg.MaxRAM != "" {
		args = append(args, "-Xmx"+cfg.MaxRAM)
	}
	args = append(args, strings.Fields(cfg.JVMArgs)...)
	args = append(args, "-jar", cfg.JarName)
	args = append(args, strings.Fields(cfg.ServerArgs)...)

	cmd := exec.Command(javaPath, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "TERM=dumb")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.setState(Stopped)
		return err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		s.setState(Stopped)
		return err
	}
	cmd.Stdout, cmd.Stderr = pw, pw

	s.Log(fmt.Sprintf("[panel] Starting: %s %s", javaPath, strings.Join(args, " ")))
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		s.setState(Stopped)
		return err
	}
	pw.Close()

	s.cmd, s.stdin, s.javaPath = cmd, stdin, javaPath
	s.done = make(chan struct{})
	s.startedAt = time.Now()
	s.wantStop = false
	s.cpuMu.Lock()
	s.lastTicks, s.lastCPUAt, s.lastCPU = 0, time.Time{}, 0
	s.cpuMu.Unlock()

	outDone := make(chan struct{})
	go func() {
		s.readOutput(pr)
		pr.Close()
		close(outDone)
	}()
	go func() {
		err := cmd.Wait()
		<-outDone
		s.exited(err)
	}()
	return nil
}

func (s *Server) readOutput(r io.Reader) {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			if i := strings.LastIndex(line, "\r"); i >= 0 {
				line = line[i+1:]
			}
			s.Log(line)
			if strings.Contains(line, "Done (") || strings.Contains(line, "Done!") {
				s.mu.Lock()
				if s.state == Starting {
					s.setState(Running)
				}
				s.mu.Unlock()
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *Server) exited(err error) {
	s.mu.Lock()
	crashed := !s.wantStop && s.state != Stopping
	code := 0
	if s.cmd != nil && s.cmd.ProcessState != nil {
		code = s.cmd.ProcessState.ExitCode()
	}
	s.cmd, s.stdin = nil, nil
	s.startedAt = time.Time{}
	close(s.done)
	s.setState(Stopped)
	restart := s.restartIn
	s.restartIn = false
	autoRestart := s.cfg.Get().AutoRestart
	s.mu.Unlock()

	msg := fmt.Sprintf("[panel] Server exited (exit code %d)", code)
	if err != nil && code == -1 {
		msg = "[panel] Server exited: " + err.Error()
	}
	s.Log(msg)

	if restart || (crashed && code != 0 && autoRestart) {
		if !restart {
			s.Log("[panel] Crash detected – restarting in 5 seconds")
			time.Sleep(5 * time.Second)
		}
		if err := s.Start(); err != nil {
			s.Log("[panel] Restart failed: " + err.Error())
		}
	}
}

// Command writes a command to the server console.
func (s *Server) Command(line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdin == nil {
		return errors.New("server is not running")
	}
	line = strings.TrimSpace(strings.ReplaceAll(line, "\n", " "))
	s.Log("> " + line)
	_, err := io.WriteString(s.stdin, line+"\n")
	return err
}

// Stop sends "stop" and force-kills the process after a timeout.
func (s *Server) Stop() error {
	s.mu.Lock()
	if s.cmd == nil {
		s.mu.Unlock()
		return errors.New("server is not running")
	}
	if s.state == Stopping {
		s.mu.Unlock()
		return nil
	}
	s.wantStop = true
	s.setState(Stopping)
	stdin, done, pid := s.stdin, s.done, s.cmd.Process.Pid
	s.mu.Unlock()

	s.Log("> stop")
	io.WriteString(stdin, "stop\n")
	go func() {
		select {
		case <-done:
		case <-time.After(90 * time.Second):
			s.Log("[panel] Server not responding – killing it")
			syscall.Kill(-pid, syscall.SIGKILL)
		}
	}()
	return nil
}

func (s *Server) Kill() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return errors.New("server is not running")
	}
	s.wantStop = true
	s.restartIn = false
	s.Log("[panel] Killing process (SIGKILL)")
	return syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
}

func (s *Server) Restart() error {
	s.mu.Lock()
	running := s.cmd != nil
	if running {
		s.restartIn = true
	}
	s.mu.Unlock()
	if !running {
		return s.Start()
	}
	return s.Stop()
}

// StopAndWait stops the server and waits for the process to exit.
func (s *Server) StopAndWait(timeout time.Duration) {
	s.mu.Lock()
	done := s.done
	running := s.cmd != nil
	s.restartIn = false
	s.mu.Unlock()
	if !running {
		return
	}
	s.Stop()
	select {
	case <-done:
	case <-time.After(timeout):
		s.Kill()
		<-done
	}
}

func (s *Server) Status() Status {
	s.mu.Lock()
	st := Status{State: s.state}
	if s.cmd != nil && s.cmd.Process != nil {
		st.PID = s.cmd.Process.Pid
		st.StartedAt = s.startedAt.Unix()
		st.Java = s.javaPath
	}
	s.mu.Unlock()
	if st.PID > 0 {
		st.CPU = s.cpuPercent(st.PID)
		st.MemoryMB = rssMB(st.PID)
	}
	return st
}

var clkTck = 100.0 // USER_HZ is practically always 100 on Linux

func (s *Server) cpuPercent(pid int) float64 {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	str := string(b)
	i := strings.LastIndexByte(str, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(str[i+1:])
	if len(f) < 13 {
		return 0
	}
	ut, _ := strconv.ParseUint(f[11], 10, 64)
	stt, _ := strconv.ParseUint(f[12], 10, 64)
	ticks := ut + stt
	now := time.Now()

	s.cpuMu.Lock()
	defer s.cpuMu.Unlock()
	if !s.lastCPUAt.IsZero() {
		dt := now.Sub(s.lastCPUAt).Seconds()
		if dt >= 0.5 {
			s.lastCPU = float64(ticks-s.lastTicks) / clkTck / dt * 100
		} else {
			return s.lastCPU
		}
	}
	s.lastTicks, s.lastCPUAt = ticks, now
	return s.lastCPU
}

func rssMB(pid int) float64 {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				kb, _ := strconv.ParseFloat(f[1], 64)
				return kb / 1024
			}
		}
	}
	return 0
}
