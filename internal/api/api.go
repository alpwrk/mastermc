// Package api provides the panel's HTTP interface and JSON API.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"mastermc/internal/auth"
	"mastermc/internal/config"
	"mastermc/internal/events"
	"mastermc/internal/files"
	"mastermc/internal/installer"
	"mastermc/internal/javart"
	"mastermc/internal/mcserver"
)

type App struct {
	Cfg    *config.Store
	Hub    *events.Hub
	Auth   *auth.Manager
	Server *mcserver.Server
	Java   *javart.Manager
	Files  *files.FS
	Web    fs.FS

	taskMu sync.Mutex
	task   Task
}

// Task describes the currently running background task (installation, Java download).
type Task struct {
	Running  bool    `json:"running"`
	Name     string  `json:"name"`
	Message  string  `json:"message"`
	Progress float64 `json:"progress"`
	Error    string  `json:"error"`
}

func New(cfg *config.Store, hub *events.Hub, am *auth.Manager, web fs.FS) *App {
	a := &App{
		Cfg:   cfg,
		Hub:   hub,
		Auth:  am,
		Java:  &javart.Manager{Dir: cfg.JavaDir()},
		Files: &files.FS{Root: cfg.ServerDir()},
		Web:   web,
	}
	a.Server = mcserver.New(cfg, hub, a.resolveJava)
	return a
}

// ---------- Background tasks ----------

func (a *App) setTask(fn func(*Task)) {
	a.taskMu.Lock()
	fn(&a.task)
	t := a.task
	a.taskMu.Unlock()
	a.Hub.Publish("task", t)
}

func (a *App) currentTask() Task {
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	return a.task
}

func (a *App) progress(msg string, pct float64) {
	a.setTask(func(t *Task) { t.Message, t.Progress = msg, pct })
}

// runTask runs fn in the background unless another task is running.
func (a *App) runTask(name string, fn func(ctx context.Context) error) error {
	a.taskMu.Lock()
	if a.task.Running {
		a.taskMu.Unlock()
		return fmt.Errorf("already running: %s", a.task.Name)
	}
	a.task = Task{Running: true, Name: name, Progress: -1}
	a.taskMu.Unlock()
	a.Hub.Publish("task", a.currentTask())
	a.Server.Log("[panel] " + name + " …")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		err := fn(ctx)
		a.setTask(func(t *Task) {
			t.Running = false
			if err != nil {
				t.Error = err.Error()
				t.Message = "Failed"
			} else {
				t.Message, t.Progress = "Done", 100
			}
		})
		if err != nil {
			a.Server.Log("[panel] " + name + " failed: " + err.Error())
		} else {
			a.Server.Log("[panel] " + name + " completed")
		}
	}()
	return nil
}

// resolveJava returns the path to the Java binary used to start the server and,
// in auto mode, installs the required version if needed.
func (a *App) resolveJava(ctx context.Context) (string, error) {
	cfg := a.Cfg.Get()
	if cfg.JavaMode != "" && cfg.JavaMode != "auto" {
		rt, err := javart.Probe(cfg.JavaMode)
		if err != nil {
			return "", err
		}
		if cfg.JavaVersion > 0 && rt.Major < cfg.JavaVersion {
			a.Server.Log(fmt.Sprintf("[panel] Warning: selected Java %d is older than the required Java %d – startup will probably fail", rt.Major, cfg.JavaVersion))
		}
		return cfg.JavaMode, nil
	}
	need := cfg.JavaVersion
	if need == 0 {
		need = 21
	}
	if rt, ok := a.Java.Find(need); ok {
		return rt.Path, nil
	}
	a.Server.Log(fmt.Sprintf("[panel] Java %d not found – installing it automatically", need))
	rt, err := a.Java.Install(ctx, need, "jre", func(msg string, pct float64) {
		a.Hub.Publish("task", Task{Running: true, Name: fmt.Sprintf("Install Java %d", need), Message: msg, Progress: pct})
	})
	a.Hub.Publish("task", a.currentTask())
	if err != nil {
		return "", err
	}
	a.Server.Log(fmt.Sprintf("[panel] Java %s installed: %s", rt.Version, rt.Path))
	return rt.Path, nil
}

// ---------- HTTP helpers ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func fail(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, os.ErrNotExist) {
		code = http.StatusNotFound
	}
	writeErr(w, code, err)
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 10<<20)).Decode(v)
}

func ok(w http.ResponseWriter) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) }

// ---------- Routing ----------

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	static := http.FileServerFS(a.Web)

	// Pages
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		if a.Auth.Valid(r) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		http.ServeFileFS(w, r, a.Web, "login.html")
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if !a.Auth.Valid(r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		http.ServeFileFS(w, r, a.Web, "index.html")
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", static))

	// Auth
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		a.Auth.Destroy(w, r)
		ok(w)
	})

	api := http.NewServeMux()
	api.HandleFunc("POST /api/password", a.changePassword)
	api.HandleFunc("GET /api/status", a.status)
	api.HandleFunc("GET /api/events", a.events)
	api.HandleFunc("POST /api/server/{action}", a.serverAction)
	api.HandleFunc("POST /api/command", a.command)
	api.HandleFunc("GET /api/versions/{type}", a.versions)
	api.HandleFunc("POST /api/install", a.install)
	api.HandleFunc("GET /api/settings", a.getSettings)
	api.HandleFunc("PUT /api/settings", a.putSettings)
	api.HandleFunc("GET /api/java", a.javaList)
	api.HandleFunc("POST /api/java/install", a.javaInstall)
	api.HandleFunc("POST /api/java/delete", a.javaDelete)
	api.HandleFunc("GET /api/files/list", a.filesList)
	api.HandleFunc("GET /api/files/read", a.filesRead)
	api.HandleFunc("PUT /api/files/write", a.filesWrite)
	api.HandleFunc("PUT /api/files/upload", a.filesUpload)
	api.HandleFunc("GET /api/files/download", a.filesDownload)
	api.HandleFunc("POST /api/files/mkdir", a.filesMkdir)
	api.HandleFunc("POST /api/files/rename", a.filesRename)
	api.HandleFunc("POST /api/files/delete", a.filesDelete)
	api.HandleFunc("POST /api/files/unzip", a.filesUnzip)
	mux.Handle("/api/", a.requireAuth(api))

	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !auth.SameOrigin(r) {
			writeErr(w, http.StatusForbidden, errors.New("invalid origin"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Auth.Valid(r) {
			writeErr(w, http.StatusUnauthorized, errors.New("not logged in"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- Auth handlers ----------

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if !a.Auth.Allowed(r) {
		writeErr(w, http.StatusTooManyRequests, errors.New("too many failed attempts – please wait 10 minutes"))
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if !auth.CheckPassword(a.Cfg.Get().PasswordHash, req.Password) {
		a.Auth.RecordFail(r)
		time.Sleep(500 * time.Millisecond)
		writeErr(w, http.StatusUnauthorized, errors.New("wrong password"))
		return
	}
	a.Auth.CreateSession(w, r)
	ok(w)
}

func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if !auth.CheckPassword(a.Cfg.Get().PasswordHash, req.Old) {
		writeErr(w, http.StatusForbidden, errors.New("current password is wrong"))
		return
	}
	if len(req.New) < 8 {
		fail(w, errors.New("the new password must be at least 8 characters long"))
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if err := a.Cfg.Update(func(s *config.Settings) { s.PasswordHash = hash }); err != nil {
		writeErr(w, 500, err)
		return
	}
	os.Remove(filepath.Join(a.Cfg.DataDir, "initial-password.txt"))
	a.Auth.DestroyAll()
	a.Auth.CreateSession(w, r)
	ok(w)
}

// ---------- Status & Events ----------

var portRe = regexp.MustCompile(`(?m)^server-port\s*=\s*(\d+)`)

func (a *App) status(w http.ResponseWriter, r *http.Request) {
	cfg := a.Cfg.Get()
	_, jarErr := os.Stat(filepath.Join(a.Cfg.ServerDir(), cfg.JarName))
	port := 25565
	if b, err := os.ReadFile(filepath.Join(a.Cfg.ServerDir(), "server.properties")); err == nil {
		if m := portRe.FindSubmatch(b); m != nil {
			port, _ = strconv.Atoi(string(m[1]))
		}
	}
	_, initialPw := os.Stat(filepath.Join(a.Cfg.DataDir, "initial-password.txt"))
	memTotal, memAvail := meminfo()
	writeJSON(w, 200, map[string]any{
		"server":          a.Server.Status(),
		"task":            a.currentTask(),
		"installed":       jarErr == nil,
		"serverType":      cfg.ServerType,
		"mcVersion":       cfg.MCVersion,
		"port":            port,
		"initialPassword": initialPw == nil,
		"host": map[string]any{
			"cpus":       runtime.NumCPU(),
			"memTotalMb": memTotal,
			"memAvailMb": memAvail,
			"arch":       runtime.GOARCH,
		},
	})
}

func meminfo() (total, avail float64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseFloat(f[1], 64)
		switch f[0] {
		case "MemTotal:":
			total = v / 1024
		case "MemAvailable:":
			avail = v / 1024
		}
	}
	return
}

// events streams log lines, state and task changes via Server-Sent Events.
func (a *App) events(w http.ResponseWriter, r *http.Request) {
	flusher, okf := w.(http.Flusher)
	if !okf {
		writeErr(w, 500, errors.New("streaming not supported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := a.Hub.Subscribe()
	defer a.Hub.Unsubscribe(ch)

	send := func(b []byte) error {
		_, err := fmt.Fprintf(w, "data: %s\n\n", b)
		return err
	}
	backlog, _ := json.Marshal(events.Event{Type: "backlog", Data: a.Server.Logs()})
	state, _ := json.Marshal(events.Event{Type: "state", Data: a.Server.State()})
	task, _ := json.Marshal(events.Event{Type: "task", Data: a.currentTask()})
	send(backlog)
	send(state)
	send(task)
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case b, open := <-ch:
			if !open {
				return
			}
			if send(b) != nil {
				return
			}
			// Send further buffered events in one batch.
		drain:
			for i := 0; i < 256; i++ {
				select {
				case b, open := <-ch:
					if !open || send(b) != nil {
						return
					}
				default:
					break drain
				}
			}
			flusher.Flush()
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ---------- Server control ----------

func (a *App) serverAction(w http.ResponseWriter, r *http.Request) {
	if a.currentTask().Running {
		fail(w, errors.New("please wait until the running task has finished"))
		return
	}
	var err error
	switch r.PathValue("action") {
	case "start":
		// Start may trigger a Java download and can therefore take a while.
		go func() {
			if err := a.Server.Start(); err != nil {
				a.Server.Log("[panel] Start failed: " + err.Error())
			}
		}()
	case "stop":
		err = a.Server.Stop()
	case "restart":
		err = a.Server.Restart()
	case "kill":
		err = a.Server.Kill()
	default:
		err = errors.New("unknown action")
	}
	if err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (a *App) command(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string `json:"command"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if err := a.Server.Command(req.Command); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

// ---------- Installation ----------

func (a *App) versions(w http.ResponseWriter, r *http.Request) {
	var (
		v   []installer.Version
		err error
	)
	switch r.PathValue("type") {
	case "vanilla":
		v, err = installer.VanillaVersions(r.Context(), r.URL.Query().Get("snapshots") == "1")
	case "paper":
		v, err = installer.PaperVersions(r.Context())
	default:
		err = errors.New("unknown type")
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, 200, v)
}

func (a *App) install(w http.ResponseWriter, r *http.Request) {
	var req installer.Request
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if !req.EULA {
		fail(w, errors.New("the Minecraft EULA must be accepted"))
		return
	}
	if req.Type != "custom" && req.Version == "" {
		fail(w, errors.New("please select a version"))
		return
	}
	label := map[string]string{"vanilla": "Vanilla", "paper": "Paper", "custom": "server jar"}[req.Type]
	name := strings.TrimSpace("Install " + label + " " + req.Version)
	err := a.runTask(name, func(ctx context.Context) error {
		if a.Server.State() != mcserver.Stopped {
			a.progress("Stopping running server", -1)
			a.Server.StopAndWait(2 * time.Minute)
		}
		dir := a.Cfg.ServerDir()
		if req.Wipe {
			a.progress("Clearing server directory", -1)
			if err := a.Files.Clear(); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		res, err := installer.Download(ctx, dir, req, a.progress)
		if err != nil {
			return err
		}
		if err := installer.WriteEULA(dir); err != nil {
			return err
		}
		if err := a.Cfg.Update(func(s *config.Settings) {
			s.JarName = res.JarName
			s.ServerType = req.Type
			s.MCVersion = res.MCVersion
			s.JavaVersion = res.JavaVersion
		}); err != nil {
			return err
		}
		a.Server.Log(fmt.Sprintf("[panel] Server jar installed, requires Java %d", res.JavaVersion))
		a.progress("Checking Java", -1)
		if _, err := a.resolveJava(ctx); err != nil {
			return err
		}
		a.progress("Starting server to generate files", -1)
		return a.Server.Start()
	})
	if err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

// ---------- Settings ----------

var ramRe = regexp.MustCompile(`^\d+[KkMmGg]?$`)

func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	s := a.Cfg.Get()
	s.PasswordHash = ""
	writeJSON(w, 200, s)
}

func (a *App) putSettings(w http.ResponseWriter, r *http.Request) {
	var in config.Settings
	if err := readJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.MinRAM != "" && !ramRe.MatchString(in.MinRAM) || in.MaxRAM != "" && !ramRe.MatchString(in.MaxRAM) {
		fail(w, errors.New("invalid RAM value (e.g. 2G or 2048M)"))
		return
	}
	if in.JarName == "" || strings.ContainsAny(in.JarName, "/\\") {
		fail(w, errors.New("invalid jar name"))
		return
	}
	if in.JavaMode == "" {
		in.JavaMode = "auto"
	}
	if in.JavaMode != "auto" {
		if _, err := javart.Probe(in.JavaMode); err != nil {
			fail(w, err)
			return
		}
	}
	if in.JavaVersion < 0 || in.JavaVersion > 99 {
		fail(w, errors.New("invalid Java version"))
		return
	}
	err := a.Cfg.Update(func(s *config.Settings) {
		s.JarName = in.JarName
		s.JavaMode = in.JavaMode
		if in.JavaVersion > 0 {
			s.JavaVersion = in.JavaVersion
		}
		s.MinRAM = strings.ToUpper(in.MinRAM)
		s.MaxRAM = strings.ToUpper(in.MaxRAM)
		s.JVMArgs = in.JVMArgs
		s.ServerArgs = in.ServerArgs
		s.Autostart = in.Autostart
		s.AutoRestart = in.AutoRestart
	})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	ok(w)
}

// ---------- Java ----------

func (a *App) javaList(w http.ResponseWriter, r *http.Request) {
	cfg := a.Cfg.Get()
	resp := map[string]any{
		"installed": a.Java.List(),
		"mode":      cfg.JavaMode,
		"required":  cfg.JavaVersion,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if av, err := a.Java.Available(ctx); err == nil {
		resp["available"] = av
	} else {
		resp["availableError"] = err.Error()
	}
	writeJSON(w, 200, resp)
}

func (a *App) javaInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Major     int    `json:"major"`
		ImageType string `json:"imageType"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	err := a.runTask(fmt.Sprintf("Install Java %d", req.Major), func(ctx context.Context) error {
		rt, err := a.Java.Install(ctx, req.Major, req.ImageType, a.progress)
		if err == nil {
			a.Server.Log(fmt.Sprintf("[panel] Java %s installed: %s", rt.Version, rt.Path))
		}
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (a *App) javaDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if st := a.Server.Status(); st.State != mcserver.Stopped && strings.HasPrefix(st.Java, filepath.Dir(filepath.Dir(req.Path))) {
		fail(w, errors.New("this Java version is currently used by the server"))
		return
	}
	if a.Cfg.Get().JavaMode == req.Path {
		fail(w, errors.New("this Java version is set as the active Java – please switch first"))
		return
	}
	if err := a.Java.Delete(req.Path); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

// ---------- Files ----------

func (a *App) filesList(w http.ResponseWriter, r *http.Request) {
	entries, err := a.Files.List(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, entries)
}

func (a *App) filesRead(w http.ResponseWriter, r *http.Request) {
	text, err := a.Files.ReadText(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"content": text})
}

func (a *App) filesWrite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, files.MaxEditSize*2)).Decode(&req); err != nil {
		fail(w, err)
		return
	}
	if err := a.Files.WriteText(req.Path, req.Content); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

// filesUpload receives the file content as the request body (one request per file).
func (a *App) filesUpload(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	f, err := a.Files.Create(rel + ".uploading")
	if err != nil {
		fail(w, err)
		return
	}
	tmp := f.Name()
	_, err = io.Copy(f, r.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		var dst string
		if dst, err = a.Files.Resolve(rel); err == nil {
			err = os.Rename(tmp, dst)
		}
	}
	if err != nil {
		os.Remove(tmp)
		fail(w, err)
		return
	}
	ok(w)
}

func (a *App) filesDownload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	names := q["name"]
	base := q.Get("path")
	if len(names) == 1 {
		p, err := a.Files.Resolve(filepath.Join(base, names[0]))
		if err != nil {
			fail(w, err)
			return
		}
		info, err := os.Stat(p)
		if err != nil {
			fail(w, err)
			return
		}
		if !info.IsDir() {
			f, err := os.Open(p)
			if err != nil {
				fail(w, err)
				return
			}
			defer f.Close()
			w.Header().Set("Content-Disposition", contentDisposition(info.Name()))
			w.Header().Set("Content-Type", "application/octet-stream")
			http.ServeContent(w, r, info.Name(), info.ModTime(), f)
			return
		}
	}
	if len(names) == 0 {
		fail(w, errors.New("no file specified"))
		return
	}
	zipName := "server-files.zip"
	if len(names) == 1 {
		zipName = names[0] + ".zip"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition(zipName))
	if err := a.Files.Zip(w, base, names); err != nil {
		a.Server.Log("[panel] ZIP download failed: " + err.Error())
	}
}

func contentDisposition(name string) string {
	safe := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, safe, urlEncode(name))
}

func urlEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

type pathReq struct {
	Path  string   `json:"path"`
	To    string   `json:"to"`
	Paths []string `json:"paths"`
}

func (a *App) filesOp(w http.ResponseWriter, r *http.Request, fn func(pathReq) error) {
	var req pathReq
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if err := fn(req); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (a *App) filesMkdir(w http.ResponseWriter, r *http.Request) {
	a.filesOp(w, r, func(q pathReq) error { return a.Files.Mkdir(q.Path) })
}

func (a *App) filesRename(w http.ResponseWriter, r *http.Request) {
	a.filesOp(w, r, func(q pathReq) error { return a.Files.Rename(q.Path, q.To) })
}

func (a *App) filesDelete(w http.ResponseWriter, r *http.Request) {
	a.filesOp(w, r, func(q pathReq) error {
		for _, p := range q.Paths {
			if err := a.Files.Delete(p); err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
		}
		return nil
	})
}

func (a *App) filesUnzip(w http.ResponseWriter, r *http.Request) {
	a.filesOp(w, r, func(q pathReq) error { return a.Files.Unzip(q.Path, q.To) })
}
