package probe

import (
	"context"

	"k8s.io/client-go/rest"
)

// RESTRaw is the production RawGetter: an absolute-path GET through the API client, so
// non-2xx answers come back as typed apierrors that Classify understands.
type RESTRaw struct {
	Client rest.Interface
}

// Get implements RawGetter.
func (r RESTRaw) Get(ctx context.Context, path string) ([]byte, error) {
	return r.Client.Get().AbsPath(path).DoRaw(ctx)
}
