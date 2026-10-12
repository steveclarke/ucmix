// Package dawschema loads Studio Pro's remote object model (dawremote.xml)
// into a typed catalog of every parameter path, with its type, range, units
// and flags. Studio Pro ships the file inside the app bundle; ucdaw reads it at
// runtime and never redistributes it.
//
// Ranged values travel on the wire normalized to 0..1: tempo 120 on the
// 10..400 range is (120-10)/390. Param converts between the musician's units
// and the wire.
package dawschema

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

// DefaultPath is where Studio Pro 8 installs its remote object model.
const DefaultPath = "/Applications/Studio Pro 8.app/Contents/PlugIns/" +
	"remoteservice.bundle/Contents/Resources/dawremote.xml"

// rootTemplate is the template the Studio Pro Remote app subscribes to.
const rootTemplate = "DAWRemote"

// Type is a parameter's value kind.
type Type string

const (
	Toggle Type = "toggle"
	Float  Type = "float"
	Int    Type = "int"
	List   Type = "list"
	String Type = "string"
	Color  Type = "color"
)

// Param is one addressable Studio Pro parameter.
type Param struct {
	Path    string   `json:"path"`
	Pattern string   `json:"pattern"`
	Name    string   `json:"name,omitempty"`
	Type    Type     `json:"type"`
	Min     float64  `json:"min"`
	Max     float64  `json:"max"`
	Default float64  `json:"default,omitempty"`
	Units   string   `json:"units,omitempty"`
	Curve   string   `json:"curve,omitempty"`
	Options []string `json:"options,omitempty"`
	Flags   []string `json:"flags,omitempty"`
}

func (p Param) hasFlag(flag string) bool {
	for _, f := range p.Flags {
		if f == flag {
			return true
		}
	}
	return false
}

// ReadOnly reports whether Studio Pro rejects writes to the parameter.
func (p Param) ReadOnly() bool { return p.hasFlag("readonly") }

// Private reports whether the parameter is internal to Studio Pro's own apps.
func (p Param) Private() bool { return p.hasFlag("private") }

// Numeric reports whether the parameter travels as a PV float.
func (p Param) Numeric() bool {
	return p.Type != String && p.Type != Color
}

// ToWire converts a value in the parameter's own units to the 0..1 wire value.
func (p Param) ToWire(value float64) (float64, error) {
	if !p.Numeric() {
		return 0, fmt.Errorf("%s is %s, not a number", p.Path, p.Type)
	}
	if value < p.Min-1e-9 || value > p.Max+1e-9 {
		return 0, fmt.Errorf(
			"%s must be between %s and %s", p.Path,
			trimFloat(p.Min), trimFloat(p.Max),
		)
	}
	if p.Max == p.Min {
		return 0, nil
	}
	return (value - p.Min) / (p.Max - p.Min), nil
}

// FromWire converts a 0..1 wire value to the parameter's own units.
func (p Param) FromWire(wire float64) float64 {
	value := p.Min + wire*(p.Max-p.Min)
	if p.Type == Int || p.Type == List || p.Type == Toggle {
		return math.Round(value)
	}
	return value
}

// Format renders a wire value the way a musician reads it.
func (p Param) Format(wire float64) string {
	value := p.FromWire(wire)
	switch p.Type {
	case Toggle:
		if value >= 0.5 {
			return "on"
		}
		return "off"
	case List:
		i := int(value)
		if i >= 0 && i < len(p.Options) {
			return p.Options[i]
		}
		return strconv.Itoa(i)
	case Int:
		return strconv.Itoa(int(value))
	}
	switch p.Units {
	case "bpm.value":
		return trimFloat(math.Round(value*100)/100) + " bpm"
	case "percent":
		return trimFloat(math.Round(value*100)) + "%"
	}
	return trimFloat(math.Round(value*1000) / 1000)
}

// ParseHuman converts a typed value ("on", "8", "96.5") to its wire value.
func (p Param) ParseHuman(text string) (float64, error) {
	text = strings.TrimSpace(text)
	switch p.Type {
	case String, Color:
		return 0, fmt.Errorf("%s is %s, not a number", p.Path, p.Type)
	case Toggle:
		switch strings.ToLower(text) {
		case "on", "true", "yes", "1":
			return 1, nil
		case "off", "false", "no", "0":
			return 0, nil
		}
		return 0, fmt.Errorf("%s is on or off, not %q", p.Path, text)
	case List:
		for i, option := range p.Options {
			if strings.EqualFold(option, text) {
				return p.ToWire(float64(i))
			}
		}
		return 0, fmt.Errorf(
			"%s is one of %s, not %q", p.Path,
			strings.Join(p.Options, ", "), text,
		)
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("%s needs a number, not %q", p.Path, text)
	}
	return p.ToWire(value)
}

func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// Catalog is every parameter Studio Pro's remote model defines.
type Catalog struct {
	params []Param
	byPath map[string]int
}

// Len reports the number of expanded parameter paths.
func (c *Catalog) Len() int { return len(c.params) }

// Lookup finds one parameter by its full path.
func (c *Catalog) Lookup(path string) (Param, bool) {
	i, ok := c.byPath[strings.Trim(path, "/")]
	if !ok {
		return Param{}, false
	}
	return c.params[i], true
}

func matches(p Param, filter string) bool {
	if filter == "" {
		return true
	}
	filter = strings.ToLower(filter)
	return strings.Contains(strings.ToLower(p.Path), filter) ||
		strings.Contains(strings.ToLower(p.Pattern), filter) ||
		strings.Contains(strings.ToLower(p.Name), filter)
}

// Filter returns every expanded parameter whose path or name contains filter.
func (c *Catalog) Filter(filter string) []Param {
	var out []Param
	for _, p := range c.params {
		if matches(p, filter) {
			out = append(out, p)
		}
	}
	return out
}

// Patterns returns one parameter per path pattern ("mixer/channel/ch<n>/volume")
// whose pattern or name contains filter, in model order.
func (c *Catalog) Patterns(filter string) []Param {
	seen := map[string]bool{}
	var out []Param
	for _, p := range c.params {
		if seen[p.Pattern] || !matches(p, filter) {
			continue
		}
		seen[p.Pattern] = true
		out = append(out, p)
	}
	return out
}

// Load reads the remote object model from path (DefaultPath when empty).
func Load(path string) (*Catalog, error) {
	if path == "" {
		path = DefaultPath
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("dawschema: opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

type node struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []node     `xml:",any"`
}

func (n node) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func (n node) child(name string) (node, bool) {
	for _, c := range n.Children {
		if c.XMLName.Local == name {
			return c, true
		}
	}
	return node{}, false
}

type builder struct {
	ranges     map[string]node
	lists      map[string][]string
	paramLists map[string]node
	templates  map[string]node
	catalog    *Catalog
}

// Parse decodes a remote object model and expands it from the DAWRemote
// template: foreach loops, template inheritance and paramlist inheritance.
func Parse(r io.Reader) (*Catalog, error) {
	var root node
	if err := xml.NewDecoder(r).Decode(&root); err != nil {
		return nil, fmt.Errorf("dawschema: parsing XML: %w", err)
	}
	b := builder{
		ranges:     map[string]node{},
		lists:      map[string][]string{},
		paramLists: map[string]node{},
		templates:  map[string]node{},
		catalog:    &Catalog{byPath: map[string]int{}},
	}
	if defs, ok := root.child("Definitions"); ok {
		for _, d := range defs.Children {
			switch d.XMLName.Local {
			case "Range":
				b.ranges[d.attr("id")] = d
			case "StringList":
				var options []string
				for _, s := range d.Children {
					options = append(options, s.attr("text"))
				}
				b.lists[d.attr("id")] = options
			}
		}
	}
	if params, ok := root.child("Parameters"); ok {
		for _, pl := range params.Children {
			b.paramLists[pl.attr("id")] = pl
		}
	}
	if templates, ok := root.child("Templates"); ok {
		for _, t := range templates.Children {
			b.templates[t.attr("id")] = t
		}
	}
	top, ok := b.templates[rootTemplate]
	if !ok {
		return nil, fmt.Errorf("dawschema: no %s template", rootTemplate)
	}
	b.expandChildren(b.templateChildren(top, 0), "", "", map[string]int{})
	return b.catalog, nil
}

func (b *builder) templateChildren(t node, depth int) []node {
	if depth > 16 {
		return nil
	}
	var out []node
	if parent := t.attr("inherit"); parent != "" {
		if p, ok := b.templates[parent]; ok {
			out = append(out, b.templateChildren(p, depth+1)...)
		}
	}
	return append(out, t.Children...)
}

func (b *builder) templateParamList(t node, depth int) string {
	if pl := t.attr("paramlist"); pl != "" || depth > 16 {
		return pl
	}
	if parent, ok := b.templates[t.attr("inherit")]; ok {
		return b.templateParamList(parent, depth+1)
	}
	return ""
}

func substitute(id string, vars map[string]int, pattern bool) string {
	for name, value := range vars {
		replacement := strconv.Itoa(value)
		if pattern {
			replacement = "<n>"
		}
		id = strings.ReplaceAll(id, name, replacement)
	}
	return id
}

func (b *builder) expandChildren(children []node, path, pattern string, vars map[string]int) {
	for _, child := range children {
		switch child.XMLName.Local {
		case "foreach":
			name := child.attr("id")
			lo, _ := strconv.Atoi(child.attr("min"))
			hi, _ := strconv.Atoi(child.attr("max"))
			for i := lo; i <= hi; i++ {
				inner := make(map[string]int, len(vars)+1)
				for k, v := range vars {
					inner[k] = v
				}
				inner[name] = i
				b.expandChildren(child.Children, path, pattern, inner)
			}
		case "Component":
			id := child.attr("id")
			b.expandComponent(
				child,
				path+substitute(id, vars, false)+"/",
				pattern+substitute(id, vars, true)+"/",
				vars,
			)
		}
	}
}

func (b *builder) expandComponent(c node, path, pattern string, vars map[string]int) {
	paramList := c.attr("paramlist")
	var templateKids []node
	if name := c.attr("template"); name != "" {
		if t, ok := b.templates[name]; ok {
			if paramList == "" {
				paramList = b.templateParamList(t, 0)
			}
			templateKids = b.templateChildren(t, 0)
		}
	}
	if paramList != "" {
		b.emitParams(paramList, path, pattern, 0)
	}
	b.expandChildren(templateKids, path, pattern, vars)
	b.expandChildren(c.Children, path, pattern, vars)
}

func (b *builder) emitParams(listID, path, pattern string, depth int) {
	list, ok := b.paramLists[listID]
	if !ok || depth > 16 {
		return
	}
	if parent := list.attr("inherit"); parent != "" {
		b.emitParams(parent, path, pattern, depth+1)
	}
	for _, p := range list.Children {
		if p.XMLName.Local != "Param" {
			continue
		}
		param := b.param(p)
		param.Path = path + p.attr("id")
		param.Pattern = pattern + p.attr("id")
		if _, dup := b.catalog.byPath[param.Path]; dup {
			continue
		}
		b.catalog.byPath[param.Path] = len(b.catalog.params)
		b.catalog.params = append(b.catalog.params, param)
	}
}

func parseFloat(s string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v
	}
	return fallback
}

func (b *builder) param(p node) Param {
	out := Param{
		Name:  p.attr("name"),
		Type:  Type(p.attr("type")),
		Units: p.attr("units"),
		Max:   1,
	}
	if flags := p.attr("flags"); flags != "" {
		out.Flags = strings.Fields(flags)
	}
	if out.Type == "" {
		out.Type = Toggle
	}
	if r, ok := b.ranges[p.attr("range")]; ok {
		out.Min = parseFloat(r.attr("min"), 0)
		out.Max = parseFloat(r.attr("max"), 1)
		out.Default = parseFloat(r.attr("def"), 0)
		out.Curve = r.attr("curve")
		if out.Units == "" {
			out.Units = r.attr("units")
		}
	}
	out.Min = parseFloat(p.attr("min"), out.Min)
	out.Max = parseFloat(p.attr("max"), out.Max)
	out.Default = parseFloat(p.attr("def"), out.Default)
	switch out.Type {
	case List:
		out.Options = b.lists[out.Units]
		out.Units = ""
		out.Min = 0
		out.Max = math.Max(float64(len(out.Options)-1), 0)
	case String, Color:
		out.Min, out.Max = 0, 0
	}
	return out
}
