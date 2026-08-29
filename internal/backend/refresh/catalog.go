package refresh

import (
	"sync"

	"github.com/petar030/ssh-native-docker-tui/internal/backend"
)

type route struct {
	page   backend.Page
	loader backend.RefreshLoader
}

// Catalog maps refresh operations and page-level refresh requests to their
// authoritative loaders. It stores routing configuration, not loaded data.
type Catalog struct {
	mu            sync.RWMutex
	routes        map[backend.RefreshKind]route
	pageRefreshes map[backend.Page]backend.RefreshKey
}

func NewCatalog() *Catalog {
	return &Catalog{
		routes:        make(map[backend.RefreshKind]route),
		pageRefreshes: make(map[backend.Page]backend.RefreshKey),
	}
}

func (catalog *Catalog) Register(
	page backend.Page,
	kind backend.RefreshKind,
	loader backend.RefreshLoader,
) error {
	if !page.Valid() || !((backend.RefreshKey{Kind: kind}).Valid()) || loader == nil {
		return &backend.AppError{Code: backend.ErrorInvalidInput, Operation: "register refresh loader", Resource: string(kind)}
	}
	catalog.mu.Lock()
	catalog.routes[kind] = route{page: page, loader: loader}
	catalog.mu.Unlock()
	return nil
}

// RegisterPage registers the complete loader used by a TUI page refresh.
// Internal targeted operations use Register.
func (catalog *Catalog) RegisterPage(
	page backend.Page,
	kind backend.RefreshKind,
	loader backend.RefreshLoader,
) error {
	if err := catalog.Register(page, kind, loader); err != nil {
		return err
	}
	catalog.mu.Lock()
	catalog.pageRefreshes[page] = backend.RefreshKey{Kind: kind}
	catalog.mu.Unlock()
	return nil
}

func (catalog *Catalog) Resolve(key backend.RefreshKey) (backend.Page, backend.RefreshLoader, bool) {
	catalog.mu.RLock()
	route, ok := catalog.routes[key.Kind]
	catalog.mu.RUnlock()
	return route.page, route.loader, ok
}

func (catalog *Catalog) ResolvePage(page backend.Page) (backend.RefreshKey, bool) {
	catalog.mu.RLock()
	key, ok := catalog.pageRefreshes[page]
	catalog.mu.RUnlock()
	return key, ok
}
