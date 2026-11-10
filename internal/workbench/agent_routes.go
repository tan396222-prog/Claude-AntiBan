package workbench

import (
	"net/http"
	"sort"
)

// Keep route discovery attached to the actual registrations. Adding a UI API
// automatically makes it visible to an external agent on that same listener.
type routeRegistrar interface {
	HandleFunc(string, func(http.ResponseWriter, *http.Request))
}
type discoverableMux struct {
	*http.ServeMux
	patterns []string
}

func newDiscoverableMux() *discoverableMux { return &discoverableMux{ServeMux: http.NewServeMux()} }
func (m *discoverableMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.ServeMux.HandleFunc(pattern, handler)
}
func (m *discoverableMux) routes() []string {
	patterns := append([]string(nil), m.patterns...)
	sort.Strings(patterns)
	return patterns
}
