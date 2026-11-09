package cage

import "github.com/sylvinus/agent-vm/internal/managedproxy"

func (c Config) RequireManagedProxy(profile string) error {
	_, err := managedproxy.Applied(profile, c.ProxyURL, c.ProxyRevision)
	return err
}
