// vmweb serves a web console for the "solaris-sparc64 session" tmux session.
//
// It attaches to tmux in control mode (tmux -C): console output arrives as %output
// notifications and goes to browsers as Server-Sent Events, keystrokes come back as
// POSTs and go to the pane with send-keys -H, and the VM buttons run vmctl. Each
// browser starts from a capture of the pane taken in-band on the control connection,
// so it sees exactly the output that follows that capture.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed static
var staticFiles embed.FS

const (
	session = "solaris"
	console = session + ":console"
)

type event struct{ name, data string }

type client struct {
	ch    chan event
	ready bool // has had its snapshot; gets output from then on
}

// reply is called with the output lines of one control-mode command.
type reply func(lines []string, failed bool)

type hub struct {
	mu      sync.Mutex
	clients map[*client]bool
	ctl     io.Writer // control-mode stdin; nil while detached
	pending []reply   // one per command sent, in order
	pane    string
	cols    int
	rows    int
	status  string // last status event (JSON)
}

func newHub() *hub {
	return &hub{clients: map[*client]bool{}, status: `{"session":false,"vm":null}`}
}

// send delivers an event to one client; a client that can't keep up is dropped and
// its browser reconnects (getting a fresh snapshot).
func (h *hub) send(c *client, ev event) {
	select {
	case c.ch <- ev:
	default:
		delete(h.clients, c)
		close(c.ch)
	}
}

func (h *hub) broadcast(ev event, readyOnly bool) {
	for c := range h.clients {
		if !readyOnly || c.ready {
			h.send(c, ev)
		}
	}
}

// command sends one control-mode command; caller holds h.mu.
func (h *hub) command(cmd string, r reply) error {
	if h.ctl == nil {
		return errors.New("console not attached")
	}
	if _, err := io.WriteString(h.ctl, cmd+"\n"); err != nil {
		return err
	}
	h.pending = append(h.pending, r)
	return nil
}

// snapshot captures the pane (with its history and the cursor) and hands it to every
// client still waiting for one. Caller holds h.mu.
func (h *hub) snapshot() {
	var info []string
	h.command(fmt.Sprintf("display-message -p -t %s '#{pane_width} #{pane_height} #{cursor_x} #{cursor_y}'", h.pane),
		func(lines []string, _ bool) { info = lines })
	h.command(fmt.Sprintf("capture-pane -p -e -S -5000 -t %s", h.pane), func(lines []string, failed bool) {
		if failed || len(info) == 0 {
			return
		}
		var w, ht, x, y int
		fmt.Sscan(info[0], &w, &ht, &x, &y)
		h.cols, h.rows = w, ht
		// The capture ends with the visible screen, so after writing it the screen
		// matches the pane; then put the cursor back.
		var b bytes.Buffer
		b.WriteString("\x1bc")
		b.WriteString(strings.Join(lines, "\r\n"))
		fmt.Fprintf(&b, "\x1b[%d;%dH", y+1, x+1)
		size := fmt.Sprintf(`{"cols":%d,"rows":%d}`, w, ht)
		data := base64.StdEncoding.EncodeToString(b.Bytes())
		for c := range h.clients {
			if !c.ready {
				h.send(c, event{"size", size})
				h.send(c, event{"reset", data})
				c.ready = true
			}
		}
	})
}

// resnapshot sends every client a fresh snapshot, e.g. after the pane is resized.
func (h *hub) resnapshot() {
	for c := range h.clients {
		c.ready = false
	}
	h.snapshot()
}

func (h *hub) add() *client {
	c := &client{ch: make(chan event, 4096)}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
	h.send(c, event{"status", h.status})
	if h.ctl != nil {
		h.snapshot()
	}
	return c
}

func (h *hub) remove(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[c] {
		delete(h.clients, c)
		close(c.ch)
	}
}

// attachLoop keeps a control-mode client attached to the session, reattaching when
// the session comes back (e.g. after the container's session restarts).
func (h *hub) attachLoop() {
	for {
		if err := h.attach(); err != nil {
			log.Printf("tmux: %v", err)
		}
		h.mu.Lock()
		h.ctl = nil
		for _, r := range h.pending {
			r(nil, true)
		}
		h.pending = nil
		for c := range h.clients {
			c.ready = false
		}
		h.mu.Unlock()
		time.Sleep(2 * time.Second)
	}
}

func (h *hub) attach() error {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", console, "#{pane_id}").Output()
	if err != nil {
		return fmt.Errorf("no session %q yet", session)
	}
	pane := strings.TrimSpace(string(out))

	cmd := exec.Command("tmux", "-C", "attach-session", "-t", session)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer cmd.Wait()
	defer stdin.Close()
	log.Printf("attached to %s (pane %s)", console, pane)

	h.mu.Lock()
	h.ctl, h.pane = stdin, pane
	h.snapshot()
	h.mu.Unlock()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	var block []string
	inBlock, ours := false, false
	for sc.Scan() {
		line := sc.Text()
		if inBlock {
			if strings.HasPrefix(line, "%end ") || strings.HasPrefix(line, "%error ") {
				inBlock = false
				// Flag 1 marks replies to this client's commands; the reply to the
				// attach itself has flag 0.
				if ours {
					h.mu.Lock()
					if len(h.pending) > 0 {
						r := h.pending[0]
						h.pending = h.pending[1:]
						if r != nil {
							r(block, strings.HasPrefix(line, "%error "))
						}
					}
					h.mu.Unlock()
				}
				continue
			}
			block = append(block, line)
			continue
		}
		switch {
		case strings.HasPrefix(line, "%begin "):
			f := strings.Fields(line)
			inBlock, ours, block = true, len(f) > 3 && f[3] == "1", nil
		case strings.HasPrefix(line, "%output "):
			rest := line[len("%output "):]
			id, data, ok := strings.Cut(rest, " ")
			if !ok || id != pane {
				continue
			}
			ev := event{"out", base64.StdEncoding.EncodeToString(unescape(data))}
			h.mu.Lock()
			h.broadcast(ev, true)
			h.mu.Unlock()
		case strings.HasPrefix(line, "%layout-change "):
			// A resize: tmux redraws the pane its own way, so start clients over.
			h.mu.Lock()
			h.resnapshot()
			h.mu.Unlock()
		case strings.HasPrefix(line, "%exit"):
			return errors.New("session ended")
		}
	}
	return sc.Err()
}

// unescape undoes control mode's \ooo escapes.
func unescape(s string) []byte {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b = append(b, byte(n))
				i += 3
				continue
			}
		}
		b = append(b, s[i])
	}
	return b
}

func (h *hub) input(data []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for len(data) > 0 {
		n := min(len(data), 256)
		var cmd strings.Builder
		cmd.WriteString("send-keys -t " + h.pane + " -H")
		for _, c := range data[:n] {
			cmd.WriteString(" " + hex.EncodeToString([]byte{c}))
		}
		if err := h.command(cmd.String(), nil); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func (h *hub) resize(cols, rows int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.command(fmt.Sprintf("resize-window -t %s -x %d -y %d", console, cols, rows), nil)
}

// statusLoop polls vmctl and pushes changes to the browsers.
func (h *hub) statusLoop(kick <-chan struct{}) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		h.updateStatus()
		select {
		case <-t.C:
		case <-kick:
		}
	}
}

func (h *hub) updateStatus() {
	alive := exec.Command("tmux", "has-session", "-t", session).Run() == nil
	vm := "null"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "vmctl", "status-json").Output(); err == nil && json.Valid(bytes.TrimSpace(out)) {
		vm = string(bytes.TrimSpace(out))
	}
	status := fmt.Sprintf(`{"session":%t,"vm":%s}`, alive, vm)
	h.mu.Lock()
	defer h.mu.Unlock()
	if status != h.status {
		h.status = status
		h.broadcast(event{"status", status}, false)
	}
}

// Actions the page may run, and whether they take an argument.
var actions = map[string]bool{
	"cd": true, "eject": false, "break": false, "pause": false, "resume": false,
	"restart": false, "quit": false,
	"snapshot-save": true, "snapshot-load": true, "snapshot-delete": true,
}

type server struct {
	hub   *hub
	token string
	hosts map[string]bool
	kick  chan struct{}
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	c := s.hub.add()
	defer s.hub.remove(c)
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case ev, ok := <-c.ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data)
			// Send whatever else is queued in the same write.
			for n := len(c.ch); n > 0; n-- {
				if ev, ok = <-c.ch; !ok {
					return
				}
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data)
			}
			fl.Flush()
		case <-ping.C:
			io.WriteString(w, ": ping\n\n")
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *server) input(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err == nil {
		err = s.hub.input(data)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) resize(w http.ResponseWriter, r *http.Request) {
	var req struct{ Cols, Rows int }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.Cols < 20 || req.Cols > 500 || req.Rows < 5 || req.Rows > 200 {
		http.Error(w, "bad size", http.StatusBadRequest)
		return
	}
	if err := s.hub.resize(req.Cols, req.Rows); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) action(w http.ResponseWriter, r *http.Request) {
	var req struct{ Action, Arg string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	takesArg, ok := actions[req.Action]
	if !ok || takesArg != (req.Arg != "") {
		http.Error(w, "unknown action or bad argument", http.StatusBadRequest)
		return
	}
	args := []string{req.Action}
	if takesArg {
		args = append(args, req.Arg)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "vmctl", args...).CombinedOutput()
	msg := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "vmctl: "))
	select {
	case s.kick <- struct{}{}:
	default:
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": err == nil, "message": msg})
}

// guard checks the token (when one is set), refuses Host names it doesn't know (so a
// DNS-rebinding page can't reach a token-less console), and requires the page's own
// header on POSTs, which a cross-site form can't send.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			got := r.URL.Query().Get("token")
			if c, err := r.Cookie("vmweb"); err == nil && got == "" {
				got = c.Value
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
				http.Error(w, "token required: open the page as /?token=...", http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Has("token") {
				http.SetCookie(w, &http.Cookie{Name: "vmweb", Value: s.token, Path: "/",
					HttpOnly: true, SameSite: http.SameSiteStrictMode})
				http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
				return
			}
		} else if !s.hostAllowed(r.Host) {
			http.Error(w, "unknown host name; set WEB_HOSTS or WEB_TOKEN", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("X-VMWeb") != "1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *server) hostAllowed(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	return net.ParseIP(host) != nil || host == "localhost" ||
		strings.HasSuffix(host, ".localhost") || s.hosts[host]
}

func post(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

func main() {
	listen := flag.String("listen", ":8080", "address to listen on")
	flag.Parse()
	log.SetPrefix("vmweb: ")
	log.SetFlags(0)

	s := &server{hub: newHub(), token: os.Getenv("WEB_TOKEN"), hosts: map[string]bool{},
		kick: make(chan struct{}, 1)}
	for _, h := range strings.FieldsFunc(os.Getenv("WEB_HOSTS"), func(r rune) bool { return r == ',' || r == ' ' }) {
		s.hosts[strings.ToLower(h)] = true
	}
	go s.hub.attachLoop()
	go s.hub.statusLoop(s.kick)

	root, _ := fs.Sub(staticFiles, "static")
	mux := http.NewServeMux()
	files := http.FileServerFS(root)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Revalidate, so a browser picks up the page from a new image.
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
	mux.HandleFunc("/api/events", s.events)
	mux.HandleFunc("/api/input", post(s.input))
	mux.HandleFunc("/api/resize", post(s.resize))
	mux.HandleFunc("/api/action", post(s.action))

	log.Printf("listening on %s", *listen)
	log.Fatal(http.ListenAndServe(*listen, s.guard(mux)))
}
