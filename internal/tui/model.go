package tui

import (
	"context"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/sratabix/multidig/internal/dnsq"
	"github.com/sratabix/multidig/internal/geo"
	"github.com/sratabix/multidig/internal/report"
	"github.com/sratabix/multidig/internal/servers"
)

type Config struct {
	Domain      string
	Types       []string
	Servers     []servers.Server
	Timeout     time.Duration
	Retries     int
	Concurrency int
	Expected    []string
	WatchEvery  time.Duration
	SourceNote  string
	Addresses   bool
}

func (c Config) QueryOptions() dnsq.Options {
	return dnsq.Options{
		Domain:      c.Domain,
		Types:       c.Types,
		Servers:     c.Servers,
		Concurrency: c.Concurrency,
		Timeout:     c.Timeout,
		Retries:     c.Retries,
		Addresses:   c.Addresses,
	}
}

type sortMode int

const (
	sortAnswer sortMode = iota
	sortRegion
	sortLatency
	sortResolver
)

func (s sortMode) String() string {
	switch s {
	case sortRegion:
		return "region"
	case sortLatency:
		return "latency"
	case sortResolver:
		return "resolver"
	default:
		return "answer"
	}
}

type resultMsg struct {
	runID int
	res   dnsq.Result
	open  bool
}

type tickMsg time.Time
type rerunMsg int

type Model struct {
	cfg   Config
	theme Theme

	width  int
	height int

	activeType int
	results    map[string][]dnsq.Result
	summaries  map[string]report.Summary

	runID   int
	running bool
	started time.Time
	elapsed time.Duration
	cancel  context.CancelFunc
	stream  <-chan dnsq.Result

	sort      sortMode
	filter    textinput.Model
	filtering bool

	rows       []dnsq.Result
	cursor     int
	offset     int
	showDetail bool
	watch      bool
	quitting   bool
}

func New(cfg Config) *Model {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 24
	}
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "region, city, resolver or answer"
	ti.SetWidth(40)

	return &Model{
		cfg:       cfg,
		theme:     NewTheme(true),
		results:   map[string][]dnsq.Result{},
		summaries: map[string]report.Summary{},
		filter:    ti,
		watch:     cfg.WatchEvery > 0,
		width:     100,
		height:    30,
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.startRun())
}

func (m *Model) startRun() tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.runID++
	m.running = true
	m.started = time.Now()
	m.elapsed = 0
	m.results = map[string][]dnsq.Result{}
	m.summaries = map[string]report.Summary{}
	m.rows = nil
	m.cursor = 0
	m.offset = 0

	m.stream = dnsq.Run(ctx, m.cfg.QueryOptions())

	return tea.Batch(waitFor(m.runID, m.stream), tick())
}

func waitFor(runID int, ch <-chan dnsq.Result) tea.Cmd {
	return func() tea.Msg {
		res, open := <-ch
		return resultMsg{runID: runID, res: res, open: open}
	}
}

func tick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.theme = NewTheme(msg.IsDark())
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampCursor()
		return m, nil

	case tickMsg:
		if m.running {
			m.elapsed = time.Since(m.started)
			return m, tick()
		}
		return m, nil

	case resultMsg:
		if msg.runID != m.runID {
			return m, nil
		}
		if !msg.open {
			m.running = false
			m.elapsed = time.Since(m.started)
			if m.watch && m.cfg.WatchEvery > 0 {
				id := m.runID
				return m, tea.Tick(m.cfg.WatchEvery, func(time.Time) tea.Msg { return rerunMsg(id) })
			}
			return m, nil
		}
		typ := msg.res.Type
		m.results[typ] = append(m.results[typ], msg.res)
		m.recompute()
		return m, waitFor(msg.runID, m.stream)

	case rerunMsg:
		if int(msg) != m.runID || !m.watch {
			return m, nil
		}
		cmd := m.startRun()
		return m, cmd

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		switch msg.String() {
		case "esc":
			m.filtering = false
			m.filter.Blur()
			m.filter.SetValue("")
			m.recompute()
			return m, nil
		case "enter":
			m.filtering = false
			m.filter.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.recompute()
		return m, cmd
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit

	case "?":
		return m, nil

	case "tab", "right", "l":
		if len(m.cfg.Types) > 1 {
			m.activeType = (m.activeType + 1) % len(m.cfg.Types)
			m.cursor, m.offset = 0, 0
			m.recompute()
		}
		return m, nil

	case "shift+tab", "left", "h":
		if len(m.cfg.Types) > 1 {
			m.activeType = (m.activeType - 1 + len(m.cfg.Types)) % len(m.cfg.Types)
			m.cursor, m.offset = 0, 0
			m.recompute()
		}
		return m, nil

	case "up", "k":
		m.cursor--
		m.clampCursor()
		return m, nil

	case "down", "j":
		m.cursor++
		m.clampCursor()
		return m, nil

	case "pgup":
		m.cursor -= m.visibleRows()
		m.clampCursor()
		return m, nil

	case "pgdown":
		m.cursor += m.visibleRows()
		m.clampCursor()
		return m, nil

	case "g", "home":
		m.cursor, m.offset = 0, 0
		return m, nil

	case "G", "end":
		m.cursor = len(m.rows) - 1
		m.clampCursor()
		return m, nil

	case "a":
		m.sort = sortAnswer
		m.recompute()
		return m, nil

	case "r":
		m.sort = sortRegion
		m.recompute()
		return m, nil

	case "t":
		m.sort = sortLatency
		m.recompute()
		return m, nil

	case "s":
		m.sort = sortResolver
		m.recompute()
		return m, nil

	case "/":
		m.filtering = true
		return m, m.filter.Focus()

	case "esc":
		if m.showDetail {
			m.showDetail = false
			return m, nil
		}
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.recompute()
		}
		return m, nil

	case "enter":
		m.showDetail = !m.showDetail
		return m, nil

	case "w":
		m.watch = !m.watch
		if m.watch && !m.running && m.cfg.WatchEvery > 0 {
			cmd := m.startRun()
			return m, cmd
		}
		return m, nil

	case "R", "ctrl+r":
		cmd := m.startRun()
		return m, cmd
	}
	return m, nil
}

func (m *Model) activeTypeName() string {
	if m.activeType < 0 || m.activeType >= len(m.cfg.Types) {
		return ""
	}
	return m.cfg.Types[m.activeType]
}

func (m *Model) recompute() {
	typ := m.activeTypeName()
	all := m.results[typ]
	m.summaries[typ] = report.Summarize(typ, len(m.cfg.Servers), all, m.cfg.Expected)
	summary := m.summaries[typ]

	needle := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	rows := make([]dnsq.Result, 0, len(all))
	for _, r := range all {
		if needle != "" && !matches(r, needle) {
			continue
		}
		rows = append(rows, r)
	}

	rank := map[string]int{}
	for i, g := range summary.Groups {
		rank[g.Key] = i
	}

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch m.sort {
		case sortRegion:
			if a.Server.Continent != b.Server.Continent {
				return geo.SortKey(a.Server.Continent) < geo.SortKey(b.Server.Continent)
			}
			if a.Server.CountryCode != b.Server.CountryCode {
				return a.Server.CountryCode < b.Server.CountryCode
			}
			return a.Server.Location() < b.Server.Location()
		case sortLatency:
			if a.Failed() != b.Failed() {
				return !a.Failed()
			}
			return a.RTT < b.RTT
		case sortResolver:
			return a.Server.IP < b.Server.IP
		default:
			if rank[a.Key()] != rank[b.Key()] {
				return rank[a.Key()] < rank[b.Key()]
			}
			if a.Server.Continent != b.Server.Continent {
				return geo.SortKey(a.Server.Continent) < geo.SortKey(b.Server.Continent)
			}
			return a.Server.Location() < b.Server.Location()
		}
	})

	m.rows = rows
	m.clampCursor()
}

func matches(r dnsq.Result, needle string) bool {
	fields := []string{
		r.Server.Continent,
		geo.Name(r.Server.Continent),
		r.Server.CountryCode,
		r.Server.City,
		r.Server.IP,
		r.Server.Name,
		r.Server.ASOrg,
		r.Key(),
		string(r.Status),
	}
	for _, f := range fields {
		if f != "" && strings.Contains(strings.ToLower(f), needle) {
			return true
		}
	}
	return false
}

func (m *Model) clampCursor() {
	if len(m.rows) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	visible := m.visibleRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
	if max := len(m.rows) - visible; m.offset > max {
		if max < 0 {
			max = 0
		}
		m.offset = max
	}
	if m.offset < 0 {
		m.offset = 0
	}
}
