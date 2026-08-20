package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sratabix/multidig/internal/dnsq"
	"github.com/sratabix/multidig/internal/geo"
	"github.com/sratabix/multidig/internal/report"
)

const maxSummaryRows = 6

func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	if m.filtering {
		v.Cursor = m.filter.Cursor()
	}
	return v
}

func (m *Model) render() string {
	if m.quitting {
		return ""
	}

	typ := m.activeTypeName()
	summary := m.summaries[typ]
	inner := m.width - 2
	if inner < 40 {
		inner = 40
	}

	var b strings.Builder
	b.WriteString(m.renderTitle(summary, inner))
	b.WriteString("\n")
	if len(m.cfg.Types) > 1 {
		b.WriteString(m.renderTabs())
		b.WriteString("\n")
	}
	b.WriteString(m.rule(inner))
	b.WriteString("\n")
	b.WriteString(m.renderSummary(summary, inner))
	b.WriteString(m.rule(inner))
	b.WriteString("\n")
	b.WriteString(m.renderTable(summary, inner))
	if m.showDetail {
		b.WriteString(m.rule(inner))
		b.WriteString("\n")
		b.WriteString(m.renderDetail(inner))
	}
	b.WriteString(m.renderFooter(inner))
	return b.String()
}

func (m *Model) rule(w int) string {
	return " " + m.theme.Rule.Render(strings.Repeat("─", w))
}

func (m *Model) renderTitle(s report.Summary, w int) string {
	kind := ""
	if len(m.cfg.Types) == 1 {
		kind = "  " + m.cfg.Types[0] + " records"
	}

	total := len(m.cfg.Servers)
	status := fmt.Sprintf("%d/%d resolvers", s.Done, total)
	switch {
	case m.running:
		status = fmt.Sprintf("querying %d/%d · %s", s.Done, total, fmtDuration(m.elapsed))
	case s.Done > 0:
		status = fmt.Sprintf("%d/%d resolvers · %s", s.Done, total, fmtDuration(m.elapsed))
	}

	refresh := ""
	if m.watch {
		refresh = " · auto-refresh " + m.cfg.WatchEvery.String()
	}

	domain := m.cfg.Domain
	width := func() int { return lipgloss.Width("multidig  " + domain + kind + status + refresh) }

	if width() > w && kind != "" {
		kind = ""
	}
	if width() > w && refresh != "" {
		refresh = " · auto " + m.cfg.WatchEvery.String()
	}
	if width() > w {
		refresh = ""
	}
	if over := width() - w; over > 0 {
		domain = fit(domain, lipgloss.Width(domain)-over)
	}

	left := m.theme.Title.Render("multidig") + "  " + m.theme.Accent.Render(domain) + m.theme.Subtle.Render(kind)
	right := m.theme.Subtle.Render(status) + m.theme.Accent.Render(refresh)

	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return " " + left + strings.Repeat(" ", gap) + right
}

func (m *Model) renderTabs() string {
	parts := make([]string, 0, len(m.cfg.Types))
	for i, t := range m.cfg.Types {
		label := " " + t + " "
		if i == m.activeType {
			parts = append(parts, m.theme.TabActive.Render(label))
		} else {
			parts = append(parts, m.theme.TabIdle.Render(label))
		}
	}
	return " " + strings.Join(parts, m.theme.Rule.Render("│"))
}

func (m *Model) renderSummary(s report.Summary, w int) string {
	if len(s.Groups) == 0 {
		return " " + m.theme.Subtle.Render("waiting for the first answer…") + "\n"
	}

	countW := len(fmt.Sprintf("%d/%d", s.Done, len(m.cfg.Servers)))
	barW := 22

	var b strings.Builder
	shown := s.Groups
	hidden := 0
	if len(shown) > maxSummaryRows {
		hidden = len(shown) - maxSummaryRows
		shown = shown[:maxSummaryRows]
	}

	keyW := 16
	for _, g := range shown {
		if n := lipgloss.Width(g.Key); n > keyW {
			keyW = n
		}
	}
	if room := w - barW - countW - 12; keyW > room {
		keyW = room
	}
	if keyW < 12 {
		keyW = 12
	}

	for _, g := range shown {
		style := m.groupStyle(s, g)
		marker := " "
		if g.Key == s.Consensus {
			marker = "▸"
		}
		line := fmt.Sprintf(" %s %s  %s  %s %s",
			m.theme.Muted.Render(marker),
			style.Render(pad(fit(g.Key, keyW), keyW)),
			m.bar(barW, g.Share),
			m.theme.Subtle.Render(padLeft(fmt.Sprintf("%d/%d", g.Count, s.Done), countW)),
			style.Render(padLeft(pct(g.Share), 4)),
		)
		b.WriteString(line)
		b.WriteString("\n")
	}
	if hidden > 0 {
		b.WriteString("   " + m.theme.Subtle.Render(fmt.Sprintf("+%d more distinct answers", hidden)) + "\n")
	}

	stats := s.ByContinent(m.results[s.Type])
	if len(stats) > 0 {
		var cells []string
		for _, c := range stats {
			share := 0.0
			if c.Total > 0 {
				share = float64(c.InSync) / float64(c.Total)
			}
			cells = append(cells, fmt.Sprintf("%s %s",
				m.theme.Muted.Render(c.Continent),
				m.theme.ShareStyle(share).Render(fmt.Sprintf("%d/%d", c.InSync, c.Total)),
			))
		}
		b.WriteString(" " + m.theme.Subtle.Render("in sync ") + strings.Join(cells, m.theme.Rule.Render(" · ")) + "\n")
	}
	return b.String()
}

func (m *Model) groupStyle(s report.Summary, g report.Group) lipgloss.Style {
	switch {
	case g.Status != dnsq.StatusOK && g.Status != dnsq.StatusEmpty:
		return m.theme.Bad
	case g.Status == dnsq.StatusEmpty:
		return m.theme.Warn
	case g.Key == s.Consensus:
		return m.theme.Good
	default:
		return m.theme.Warn
	}
}

func (m *Model) bar(w int, share float64) string {
	if w < 4 {
		w = 4
	}
	filled := int(share*float64(w) + 0.5)
	if filled > w {
		filled = w
	}
	if filled == 0 && share > 0 {
		filled = 1
	}
	return m.theme.ShareStyle(share).Render(strings.Repeat("█", filled)) +
		m.theme.Rule.Render(strings.Repeat("░", w-filled))
}

type columns struct {
	glyph    int
	region   int
	location int
	resolver int
	answer   int
	network  int
	rtt      int
}

const maxAnswerCol = 40

func (m *Model) columns(w int) columns {
	c := columns{glyph: 1, region: 6, location: 16, resolver: 16, rtt: 5}
	for _, r := range m.rows {
		if n := lipgloss.Width(r.Server.IP); n > c.resolver {
			c.resolver = n
		}
	}
	c.resolver = min(c.resolver, 24)

	spare := w - (c.glyph + c.region + c.location + c.resolver + c.rtt + 6)
	if spare < 14 {
		shrink := min(14-spare, c.location-8)
		c.location -= shrink
		spare += shrink
	}
	c.answer = spare
	if rest := spare - min(spare, maxAnswerCol) - 1; rest >= 10 {
		c.network = min(rest, 26)
		c.answer = spare - c.network - 1
	}
	c.answer = max(c.answer, 8)
	return c
}

func (m *Model) renderTable(s report.Summary, w int) string {
	c := m.columns(w)
	var b strings.Builder

	header := fmt.Sprintf(" %s %s %s %s %s",
		pad("", c.glyph),
		pad("REGION", c.region),
		pad("LOCATION", c.location),
		pad("RESOLVER", c.resolver),
		pad("ANSWER", c.answer),
	)
	if c.network > 0 {
		header += " " + pad("NETWORK", c.network)
	}
	header += " " + padLeft("MS", c.rtt)
	b.WriteString(m.theme.Header.Render(pad(header, w+1)))
	b.WriteString("\n")

	visible := m.visibleRows()
	if len(m.rows) == 0 {
		msg := "no resolvers match this filter"
		if s.Done == 0 {
			msg = "querying…"
		}
		b.WriteString(" " + m.theme.Subtle.Render(msg) + "\n")
		for i := 1; i < visible; i++ {
			b.WriteString("\n")
		}
		return b.String()
	}

	end := min(m.offset+visible, len(m.rows))
	for i := m.offset; i < end; i++ {
		b.WriteString(m.renderRow(s, m.rows[i], c, i == m.cursor, w))
		b.WriteString("\n")
	}
	for i := end - m.offset; i < visible; i++ {
		b.WriteString("\n")
	}
	return b.String()
}

func (m *Model) renderRow(s report.Summary, r dnsq.Result, c columns, selected bool, w int) string {
	region := r.Server.Continent
	if r.Server.CountryCode != "" && r.Server.CountryCode != r.Server.Continent {
		region += "/" + r.Server.CountryCode
	}

	answerStyle := m.theme.Warn
	glyph := "◆"
	switch {
	case r.Failed():
		answerStyle = m.theme.Bad
		glyph = "✕"
	case s.InConsensus(r):
		answerStyle = m.theme.Good
		glyph = "●"
	case r.Status == dnsq.StatusEmpty:
		answerStyle = m.theme.Warn
		glyph = "○"
	}

	rtt := "—"
	if r.RTT > 0 && !r.Failed() {
		rtt = fmt.Sprintf("%d", r.RTT.Milliseconds())
	}

	if selected {
		plain := fmt.Sprintf(" %s %s %s %s %s",
			pad(glyph, c.glyph),
			pad(fit(region, c.region), c.region),
			pad(fit(r.Server.Location(), c.location), c.location),
			pad(fit(r.Server.IP, c.resolver), c.resolver),
			pad(fit(r.Key(), c.answer), c.answer),
		)
		if c.network > 0 {
			plain += " " + pad(fit(r.Server.ASOrg, c.network), c.network)
		}
		plain += " " + padLeft(rtt, c.rtt)
		return m.theme.Cursor.Render(pad(plain, w+1))
	}

	line := fmt.Sprintf(" %s %s %s %s %s",
		answerStyle.Render(pad(glyph, c.glyph)),
		m.theme.Muted.Render(pad(fit(region, c.region), c.region)),
		m.theme.Row.Render(pad(fit(r.Server.Location(), c.location), c.location)),
		m.theme.Subtle.Render(pad(fit(r.Server.IP, c.resolver), c.resolver)),
		answerStyle.Render(pad(fit(r.Key(), c.answer), c.answer)),
	)
	if c.network > 0 {
		line += " " + m.theme.Rule.Render(pad(fit(r.Server.ASOrg, c.network), c.network))
	}
	return line + " " + m.theme.Subtle.Render(padLeft(rtt, c.rtt))
}

func (m *Model) renderDetail(w int) string {
	if len(m.rows) == 0 {
		return " " + m.theme.Subtle.Render("nothing selected") + "\n"
	}
	r := m.rows[min(m.cursor, len(m.rows)-1)]

	label := func(k string) string { return m.theme.Subtle.Render(pad(k, 12)) }
	lines := []string{
		" " + label("resolver") + m.theme.Title.Render(r.Server.IP) + m.theme.Subtle.Render("  "+r.Server.Name),
		" " + label("location") + m.theme.Row.Render(fmt.Sprintf("%s, %s (%s)", r.Server.Location(), r.Server.CountryCode, geo.Name(r.Server.Continent))),
		" " + label("network") + m.theme.Row.Render(orDash(r.Server.ASOrg)) + m.theme.Subtle.Render(fmt.Sprintf("  reliability %.0f%%  dnssec %v", r.Server.Reliability*100, r.Server.DNSSEC)),
		" " + label("status") + m.statusLine(r),
	}
	answers := r.Answers
	if len(answers) == 0 {
		answers = []string{r.Key()}
	}
	lines = append(lines, " "+label(r.Type+" answer")+m.theme.Row.Render(fit(strings.Join(answers, "  "), w-13)))
	if len(r.Records) > 0 {
		lines = append(lines, " "+label("chain")+m.theme.Subtle.Render(fit(strings.Join(r.Records, " · "), w-13)))
	}
	if r.Err != nil {
		lines = append(lines, " "+label("error")+m.theme.Bad.Render(fit(r.Err.Error(), w-13)))
	}
	return strings.Join(lines, "\n") + "\n"
}

func (m *Model) statusLine(r dnsq.Result) string {
	style := m.theme.Good
	if r.Failed() {
		style = m.theme.Bad
	} else if r.Status == dnsq.StatusEmpty {
		style = m.theme.Warn
	}
	out := style.Render(string(r.Status))
	if r.RTT > 0 {
		out += m.theme.Subtle.Render(fmt.Sprintf("  %dms", r.RTT.Milliseconds()))
	}
	return out
}

func (m *Model) renderFooter(w int) string {
	if m.filtering || m.filter.Value() != "" {
		hint := m.theme.Subtle.Render("  enter apply · esc clear")
		return " " + m.filter.View() + hint
	}

	type hint struct {
		text string
		drop int
	}
	hints := []hint{}
	if len(m.cfg.Types) > 1 {
		hints = append(hints, hint{"tab type", 3})
	}
	hints = append(hints,
		hint{"↑↓ move", 5},
		hint{"enter detail", 4},
		hint{"←→ sort:" + m.sort.String(), 2},
		hint{"/ filter", 6},
		hint{"R rerun", 1},
	)
	state := "off"
	if m.watch {
		state = "on " + m.cfg.WatchEvery.String()
	}
	hints = append(hints, hint{"w auto:" + state, 7})
	hints = append(hints, hint{"q quit", 99})

	for {
		total := 0
		for i, h := range hints {
			total += lipgloss.Width(h.text)
			if i > 0 {
				total += 3
			}
		}
		if total+1 <= w || len(hints) == 1 {
			break
		}
		lowest, at := 100, -1
		for i, h := range hints {
			if h.drop < lowest {
				lowest, at = h.drop, i
			}
		}
		hints = append(hints[:at], hints[at+1:]...)
	}

	keys := make([]string, 0, len(hints))
	for _, h := range hints {
		keys = append(keys, h.text)
	}
	line := " " + m.theme.Help.Render(strings.Join(keys, m.theme.Rule.Render(" · ")))
	if m.cfg.SourceNote != "" && lipgloss.Width(line)+lipgloss.Width(m.cfg.SourceNote)+4 < w {
		gap := w - lipgloss.Width(line) - lipgloss.Width(m.cfg.SourceNote) + 1
		line += strings.Repeat(" ", gap) + m.theme.Rule.Render(m.cfg.SourceNote)
	}
	return line
}

func (m *Model) chromeHeight() int {
	typ := m.activeTypeName()
	s := m.summaries[typ]

	h := 1
	if len(m.cfg.Types) > 1 {
		h++
	}
	h++

	groups := len(s.Groups)
	if groups == 0 {
		h++
	} else {
		h += min(groups, maxSummaryRows) + 1
		if groups > maxSummaryRows {
			h++
		}
	}

	h += 2

	if m.showDetail {
		h++
		if len(m.rows) == 0 {
			h++
		} else {
			r := m.rows[min(m.cursor, len(m.rows)-1)]
			h += 5
			if len(r.Records) > 0 {
				h++
			}
			if r.Err != nil {
				h++
			}
		}
	}
	return h + 1
}

func (m *Model) visibleRows() int {
	v := m.height - m.chromeHeight()
	if v < 3 {
		v = 3
	}
	return v
}

func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > w {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

func pad(s string, w int) string {
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

func padLeft(s string, w int) string {
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return strings.Repeat(" ", gap) + s
}

func pct(share float64) string {
	return fmt.Sprintf("%d%%", int(share*100+0.5))
}

func fmtDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
