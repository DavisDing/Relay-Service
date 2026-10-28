package monitor

import (
	"bytes"
	"encoding/json"
	"errors"
	"html"
	"io"
	"mime"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const publicStatusBodyLimit = 1 << 20
const publicStatusItemLimit = 200

var ErrPublicStatusUnsupported = errors.New("公开状态页未返回支持的状态结构")
var ErrPublicStatusConfig = errors.New("公开状态页解析配置无效")

// PublicStatusConfig selects a fixed GET resource and explicit field mappings.
// Paths are dot separated object keys, without expressions, recursion or scripts.
type PublicStatusConfig struct {
	Format              string   `json:"format,omitempty"`
	ResourcePath        string   `json:"resource_path,omitempty"`
	ItemsPath           string   `json:"items_path,omitempty"`
	NameField           string   `json:"name_field,omitempty"`
	StatusField         string   `json:"status_field,omitempty"`
	HealthyValues       []string `json:"healthy_values,omitempty"`
	UnhealthyValues     []string `json:"unhealthy_values,omitempty"`
	HTMLItemTag         string   `json:"html_item_tag,omitempty"`
	HTMLItemAttribute   string   `json:"html_item_attribute,omitempty"`
	HTMLItemValue       string   `json:"html_item_value,omitempty"`
	HTMLNameAttribute   string   `json:"html_name_attribute,omitempty"`
	HTMLStatusAttribute string   `json:"html_status_attribute,omitempty"`
}

type PublicStatus struct {
	Source     string                  `json:"source"`
	Components []PublicStatusComponent `json:"components"`
	Count      int                     `json:"count"`
	Truncated  bool                    `json:"truncated"`
	ObservedAt time.Time               `json:"observed_at"`
	UpdatedAt  *time.Time              `json:"updated_at,omitempty"`
	Unknown    bool                    `json:"unknown"`
	Unhealthy  bool                    `json:"unhealthy"`
}

type PublicStatusComponent struct {
	Name         string     `json:"name"`
	Status       string     `json:"status"`
	SourceStatus string     `json:"source_status,omitempty"`
	Category     string     `json:"category,omitempty"`
	Group        string     `json:"group,omitempty"`
	Uptime       *float64   `json:"uptime,omitempty"`
	Heartbeats   []string   `json:"heartbeats,omitempty"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

func publicStatusFormat(f string) string {
	switch f {
	case "", "auto":
		return "auto"
	case "status_json":
		return "json"
	case "status_html":
		return "html"
	}
	return f
}

// ValidatePublicStatusConfig does not resolve or contact a URL. The caller must
// still apply its normal SSRF, authorization, timeout and response-size guards.
func ValidatePublicStatusConfig(c *PublicStatusConfig) error {
	if c == nil {
		return nil
	}
	switch publicStatusFormat(c.Format) {
	case "auto", "statuspage", "pipio", "json", "html":
	default:
		return ErrPublicStatusConfig
	}
	if c.ResourcePath != "" {
		u, err := url.Parse(c.ResourcePath)
		if err != nil || !strings.HasPrefix(c.ResourcePath, "/") || strings.HasPrefix(c.ResourcePath, "//") || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(c.ResourcePath, "\\\r\n") || len(c.ResourcePath) > 512 {
			return ErrPublicStatusConfig
		}
		for _, part := range strings.Split(u.Path, "/") {
			if part == "." || part == ".." {
				return ErrPublicStatusConfig
			}
		}
	}
	for i, path := range []string{c.ItemsPath, c.NameField, c.StatusField} {
		if i == 0 && path == "$" {
			continue
		}
		if path == "" {
			continue
		}
		if len(path) > 256 || len(strings.Split(path, ".")) > 12 {
			return ErrPublicStatusConfig
		}
		for _, part := range strings.Split(path, ".") {
			if !publicStatusIdentifier(part) {
				return ErrPublicStatusConfig
			}
		}
	}
	if len(c.HealthyValues) > 32 || len(c.UnhealthyValues) > 32 {
		return ErrPublicStatusConfig
	}
	seen := map[string]bool{}
	for _, values := range [][]string{c.HealthyValues, c.UnhealthyValues} {
		for _, value := range values {
			key := statusText(value)
			if key == "" || len(value) > 128 || seen[key] || !safeName(value) {
				return ErrPublicStatusConfig
			}
			seen[key] = true
		}
	}
	for _, name := range []string{c.HTMLItemTag, c.HTMLItemAttribute, c.HTMLNameAttribute, c.HTMLStatusAttribute} {
		if name != "" && !publicStatusIdentifier(name) {
			return ErrPublicStatusConfig
		}
	}
	if c.HTMLItemValue != "" && (!safeName(c.HTMLItemValue) || len(c.HTMLItemValue) > 128) {
		return ErrPublicStatusConfig
	}
	if publicStatusFormat(c.Format) == "html" && (c.HTMLItemTag == "" || c.HTMLItemAttribute == "" || c.HTMLNameAttribute == "" || len(c.HealthyValues) == 0 || len(c.UnhealthyValues) == 0) {
		return ErrPublicStatusConfig
	}
	return nil
}

func publicStatusIdentifier(s string) bool {
	if len(s) == 0 || len(s) > 80 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == ':') {
			return false
		}
	}
	return true
}
func statusText(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
func publicStatusMapped(v string, c *PublicStatusConfig) string {
	key := statusText(v)
	if len(c.HealthyValues) > 0 || len(c.UnhealthyValues) > 0 {
		for _, x := range c.HealthyValues {
			if key == statusText(x) {
				return "healthy"
			}
		}
		for _, x := range c.UnhealthyValues {
			if key == statusText(x) {
				return "unhealthy"
			}
		}
		return "unknown"
	}
	switch key {
	case "operational", "healthy", "ok", "up", "passing":
		return "healthy"
	case "degraded_performance", "partial_outage", "major_outage", "under_maintenance", "degraded", "unhealthy", "down", "outage", "maintenance", "failing":
		return "unhealthy"
	}
	return "unknown"
}

func ParsePublicStatus(format string, body []byte, contentType string, cfg *PublicStatusConfig, now time.Time) (*PublicStatus, error) {
	c := PublicStatusConfig{}
	if cfg != nil {
		c = *cfg
	}
	if format != "" {
		c.Format = format
	}
	if err := ValidatePublicStatusConfig(&c); err != nil {
		return nil, err
	}
	if len(body) == 0 || len(body) > publicStatusBodyLimit {
		return nil, ErrPublicStatusUnsupported
	}
	f := publicStatusFormat(c.Format)
	media, _, _ := mime.ParseMediaType(contentType)
	if f == "auto" {
		if media == "text/html" || bytes.HasPrefix(bytes.TrimSpace(body), []byte("<")) {
			f = "html"
		} else {
			f = "json"
		}
	}
	result := &PublicStatus{ObservedAt: now.UTC(), Components: []PublicStatusComponent{}}
	result.Source = c.ResourcePath
	if result.Source == "" {
		switch f {
		case "pipio":
			result.Source = "/api/uptime/status"
		case "statuspage":
			result.Source = "/api/v2/summary.json"
		default:
			result.Source = "/"
		}
	}
	var err error
	if f == "html" {
		err = parsePublicStatusHTML(body, &c, result)
	} else {
		var root any
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		var decodeErr error
		root, decodeErr = publicJSONValue(dec, 0)
		_, trailingErr := dec.Token()
		if decodeErr != nil || trailingErr != io.EOF {
			return nil, ErrPublicStatusUnsupported
		}
		obj, _ := root.(map[string]any)
		if f == "pipio" || (f == "json" && c.ItemsPath == "" && obj != nil && isPipioStatus(obj)) {
			if c.ResourcePath == "" {
				result.Source = "/api/uptime/status"
			}
			err = parsePipioStatus(obj, result)
		} else {
			standard := f == "statuspage" || (c.ItemsPath == "" && c.NameField == "" && c.StatusField == "" && len(c.HealthyValues) == 0 && len(c.UnhealthyValues) == 0)
			err = parsePublicStatusJSON(root, &c, result, standard)
		}
	}
	if err != nil {
		return nil, err
	}
	if result.Count == 0 {
		result.Unknown = true
	}
	result.Truncated = result.Count > publicStatusItemLimit
	return result, nil
}

// Reject duplicate JSON keys and excessive nesting rather than letting a later
// healthy value silently replace an earlier failure in an ambiguous document.
func publicJSONValue(dec *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, ErrPublicStatusUnsupported
	}
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		obj := map[string]any{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, ErrPublicStatusUnsupported
			}
			if _, exists := obj[name]; exists {
				return nil, ErrPublicStatusUnsupported
			}
			value, err := publicJSONValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			obj[name] = value
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return nil, ErrPublicStatusUnsupported
		}
		return obj, nil
	case json.Delim('['):
		values := []any{}
		for dec.More() {
			value, err := publicJSONValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim(']') {
			return nil, ErrPublicStatusUnsupported
		}
		return values, nil
	default:
		if _, ok := token.(json.Delim); ok {
			return nil, ErrPublicStatusUnsupported
		}
		return token, nil
	}
}

func addPublicStatus(p *PublicStatus, c PublicStatusComponent) error {
	if !safeName(c.Name) || (c.Category != "" && !safeName(c.Category)) || (c.Group != "" && !safeName(c.Group)) {
		return ErrPublicStatusUnsupported
	}
	p.Count++
	p.Unknown = p.Unknown || c.Status == "unknown"
	p.Unhealthy = p.Unhealthy || c.Status == "unhealthy"
	if len(p.Components) < publicStatusItemLimit {
		p.Components = append(p.Components, c)
	}
	return nil
}
func jsonAt(v any, path string) any {
	if path == "" || path == "$" {
		return v
	}
	for _, key := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}
func scalarStatus(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case json.Number:
		return string(v)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}
func sourceUpdated(v any) *time.Time {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}
func parsePublicStatusJSON(root any, c *PublicStatusConfig, p *PublicStatus, standard bool) error {
	path, name, status := c.ItemsPath, c.NameField, c.StatusField
	if path == "" {
		path = "components"
	}
	if name == "" {
		name = "name"
	}
	if status == "" {
		status = "status"
	}
	items, ok := jsonAt(root, path).([]any)
	if !ok {
		return ErrPublicStatusUnsupported
	}
	p.UpdatedAt = sourceUpdated(jsonAt(root, "page.updated_at"))
	if p.UpdatedAt == nil {
		p.UpdatedAt = sourceUpdated(jsonAt(root, "updated_at"))
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return ErrPublicStatusUnsupported
		}
		if group, _ := m["group"].(bool); standard && group {
			continue
		}
		n, ok := jsonAt(item, name).(string)
		if !ok {
			return ErrPublicStatusUnsupported
		}
		raw := scalarStatus(jsonAt(item, status))
		normalized := publicStatusMapped(raw, c)
		if standard {
			switch raw {
			case "operational":
				normalized = "healthy"
			case "degraded_performance", "partial_outage", "major_outage", "under_maintenance":
				normalized = "unhealthy"
			default:
				normalized = "unknown"
			}
		}
		// Unknown values are deliberately not echoed: remote errors may contain secrets.
		component := PublicStatusComponent{Name: n, Status: normalized, UpdatedAt: sourceUpdated(m["updated_at"])}
		if normalized != "unknown" && len(raw) <= 128 && safeName(raw) {
			component.SourceStatus = raw
		}
		if err := addPublicStatus(p, component); err != nil {
			return err
		}
	}
	return nil
}
func isPipioStatus(m map[string]any) bool {
	groups, ok := m["data"].([]any)
	if !ok || len(groups) == 0 {
		return false
	}
	g, ok := groups[0].(map[string]any)
	if !ok {
		return false
	}
	_, ok = g["monitors"].([]any)
	return ok
}
func pipioState(v any) (string, string) {
	n, ok := v.(json.Number)
	if !ok {
		return "unknown", "unknown"
	}
	switch string(n) {
	case "0":
		return "unhealthy", "outage"
	case "1":
		return "healthy", "operational"
	case "2":
		return "unknown", "pending"
	case "3":
		return "unhealthy", "maintenance"
	}
	return "unknown", "unknown"
}
func parsePipioStatus(root map[string]any, p *PublicStatus) error {
	success, ok := root["success"].(bool)
	if !ok || !success {
		return ErrPublicStatusUnsupported
	}
	groups, ok := root["data"].([]any)
	if !ok {
		return ErrPublicStatusUnsupported
	}
	for _, g := range groups {
		group, ok := g.(map[string]any)
		if !ok {
			return ErrPublicStatusUnsupported
		}
		category, ok := group["categoryName"].(string)
		if !ok || !safeName(category) {
			return ErrPublicStatusUnsupported
		}
		monitors, ok := group["monitors"].([]any)
		if !ok {
			return ErrPublicStatusUnsupported
		}
		for _, v := range monitors {
			m, ok := v.(map[string]any)
			if !ok {
				return ErrPublicStatusUnsupported
			}
			name, ok := m["name"].(string)
			if !ok {
				return ErrPublicStatusUnsupported
			}
			status, source := pipioState(m["status"])
			component := PublicStatusComponent{Name: name, Status: status, SourceStatus: source, Category: category}
			component.Group, _ = m["group"].(string)
			if n, ok := m["uptime"].(json.Number); ok {
				if f, err := n.Float64(); err == nil && f >= 0 && f <= 1 {
					component.Uptime = &f
				}
			}
			if heartbeats, ok := m["heartbeats"].([]any); ok {
				start := len(heartbeats) - 24
				if start < 0 {
					start = 0
				}
				for _, h := range heartbeats[start:] {
					state, _ := pipioState(h)
					component.Heartbeats = append(component.Heartbeats, state)
				}
			}
			if err := addPublicStatus(p, component); err != nil {
				return err
			}
		}
	}
	return nil
}

// The bounded HTML scanner reads only explicit tags/attributes and visible text.
// It never executes code, follows resources, interprets CSS or applies regexes.
// Malformed/ambiguous markup fails closed rather than manufacturing availability.
type publicHTMLToken struct {
	tag       string
	attrs     map[string]string
	text      string
	end, self bool
}

func nextPublicHTML(s string) (publicHTMLToken, string, error) {
	if s == "" {
		return publicHTMLToken{}, "", io.EOF
	}
	if s[0] != '<' {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			i = len(s)
		}
		return publicHTMLToken{text: html.UnescapeString(s[:i])}, s[i:], nil
	}
	if strings.HasPrefix(s, "<!--") {
		i := strings.Index(s[4:], "-->")
		if i < 0 {
			return publicHTMLToken{}, "", ErrPublicStatusUnsupported
		}
		return publicHTMLToken{}, s[i+7:], nil
	}
	quote := byte(0)
	end := -1
	for i := 1; i < len(s); i++ {
		ch := s[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
		} else if ch == '>' {
			end = i
			break
		}
	}
	if end < 0 {
		return publicHTMLToken{}, "", ErrPublicStatusUnsupported
	}
	raw := strings.TrimSpace(s[1:end])
	rest := s[end+1:]
	t := publicHTMLToken{attrs: map[string]string{}}
	if strings.HasPrefix(raw, "!") || strings.HasPrefix(raw, "?") {
		return t, rest, nil
	}
	if strings.HasPrefix(raw, "/") {
		t.end = true
		raw = strings.TrimSpace(raw[1:])
	}
	if strings.HasSuffix(raw, "/") {
		t.self = true
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "/"))
	}
	i := 0
	for i < len(raw) && !unicode.IsSpace(rune(raw[i])) {
		i++
	}
	t.tag = strings.ToLower(raw[:i])
	if !publicStatusIdentifier(t.tag) {
		return t, "", ErrPublicStatusUnsupported
	}
	raw = raw[i:]
	if t.end {
		if strings.TrimSpace(raw) != "" {
			return t, "", ErrPublicStatusUnsupported
		}
		return t, rest, nil
	}
	for strings.TrimSpace(raw) != "" {
		raw = strings.TrimSpace(raw)
		i = 0
		for i < len(raw) && !unicode.IsSpace(rune(raw[i])) && raw[i] != '=' {
			i++
		}
		key := strings.ToLower(raw[:i])
		if !publicStatusIdentifier(key) {
			return t, "", ErrPublicStatusUnsupported
		}
		raw = strings.TrimSpace(raw[i:])
		value := ""
		if strings.HasPrefix(raw, "=") {
			raw = strings.TrimSpace(raw[1:])
			if raw == "" {
				return t, "", ErrPublicStatusUnsupported
			}
			if raw[0] == '\'' || raw[0] == '"' {
				q := raw[0]
				j := strings.IndexByte(raw[1:], q)
				if j < 0 {
					return t, "", ErrPublicStatusUnsupported
				}
				value = raw[1 : j+1]
				raw = raw[j+2:]
			} else {
				i = 0
				for i < len(raw) && !unicode.IsSpace(rune(raw[i])) {
					i++
				}
				value = raw[:i]
				raw = raw[i:]
			}
		}
		if _, exists := t.attrs[key]; exists {
			return t, "", ErrPublicStatusUnsupported
		}
		t.attrs[key] = html.UnescapeString(value)
	}
	return t, rest, nil
}
func publicHTMLClosing(s, closing string) int {
	for offset := 0; offset < len(s); {
		i := strings.IndexByte(s[offset:], '<')
		if i < 0 {
			return -1
		}
		i += offset
		end := i + len(closing)
		if end <= len(s) && strings.EqualFold(s[i:end], closing) && (end == len(s) || s[end] == '>' || unicode.IsSpace(rune(s[end]))) {
			return i
		}
		offset = i + 1
	}
	return -1
}

func publicHTMLIgnored(tag string) bool {
	switch tag {
	case "script", "style", "template", "noscript", "nav", "header", "footer", "head":
		return true
	}
	return false
}
func publicHTMLBlock(tag string) bool {
	switch tag {
	case "div", "p", "li", "ul", "ol", "section", "article", "main", "table", "tr", "td", "th", "br", "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	return false
}

func publicHTMLVoid(tag string) bool {
	switch tag {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}
func parsePublicStatusHTML(body []byte, c *PublicStatusConfig, p *PublicStatus) error {
	if c.HTMLItemTag == "" || c.HTMLItemAttribute == "" || c.HTMLNameAttribute == "" || len(c.HealthyValues) == 0 || len(c.UnhealthyValues) == 0 {
		return ErrPublicStatusConfig
	}
	rest := string(body)
	type frame struct {
		tag     string
		ignored bool
	}
	stack := []frame{}
	ignored := 0
	candidateDepth := -1
	candidate := PublicStatusComponent{}
	var text strings.Builder
	statusValue := ""
	for rest != "" {
		// Raw text elements have HTML semantics, so '<' inside JS/CSS is not markup.
		if len(stack) > 0 && (stack[len(stack)-1].tag == "script" || stack[len(stack)-1].tag == "style") {
			closing := "</" + stack[len(stack)-1].tag
			i := publicHTMLClosing(rest, closing)
			if i < 0 {
				return ErrPublicStatusUnsupported
			}
			rest = rest[i:]
		}
		token, next, err := nextPublicHTML(rest)
		if err != nil {
			return err
		}
		rest = next
		if token.tag == "" {
			if candidateDepth >= 0 && ignored == 0 {
				if text.Len()+len(token.text) > 4096 {
					return ErrPublicStatusUnsupported
				}
				text.WriteString(token.text)
			}
			continue
		}
		if token.end {
			if candidateDepth >= 0 && ignored == 0 && publicHTMLBlock(token.tag) {
				text.WriteByte(' ')
			}
			if len(stack) == 0 || stack[len(stack)-1].tag != token.tag {
				return ErrPublicStatusUnsupported
			}
			if candidateDepth == len(stack)-1 {
				if c.HTMLStatusAttribute == "" {
					statusValue = text.String()
				}
				candidate.Status = publicStatusMapped(statusValue, c)
				if err := addPublicStatus(p, candidate); err != nil {
					return err
				}
				candidateDepth = -1
			}
			if stack[len(stack)-1].ignored {
				ignored--
			}
			stack = stack[:len(stack)-1]
			continue
		}
		if candidateDepth >= 0 && ignored == 0 && publicHTMLBlock(token.tag) {
			text.WriteByte(' ')
		}
		hidden := token.attrs["hidden"] != ""
		if _, exists := token.attrs["hidden"]; exists {
			hidden = true
		}
		hidden = hidden || strings.EqualFold(token.attrs["aria-hidden"], "true")
		style := strings.Join(strings.Fields(strings.ToLower(token.attrs["style"])), "")
		hidden = hidden || strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden")
		skip := publicHTMLIgnored(token.tag) || hidden
		matches := token.tag == strings.ToLower(c.HTMLItemTag)
		value, exists := token.attrs[strings.ToLower(c.HTMLItemAttribute)]
		matches = matches && exists && (c.HTMLItemValue == "" || value == c.HTMLItemValue)
		if matches && ignored == 0 && !skip {
			if candidateDepth >= 0 {
				return ErrPublicStatusUnsupported
			}
			candidate = PublicStatusComponent{Name: token.attrs[strings.ToLower(c.HTMLNameAttribute)]}
			text.Reset()
			statusValue = token.attrs[strings.ToLower(c.HTMLStatusAttribute)]
			candidateDepth = len(stack)
		}
		if token.self || publicHTMLVoid(token.tag) {
			if candidateDepth == len(stack) {
				candidate.Status = publicStatusMapped(statusValue, c)
				if err := addPublicStatus(p, candidate); err != nil {
					return err
				}
				candidateDepth = -1
			}
			continue
		}
		if len(stack) >= 256 {
			return ErrPublicStatusUnsupported
		}
		stack = append(stack, frame{token.tag, skip})
		if skip {
			ignored++
		}
	}
	if candidateDepth >= 0 || len(stack) > 0 {
		return ErrPublicStatusUnsupported
	}
	return nil
}
