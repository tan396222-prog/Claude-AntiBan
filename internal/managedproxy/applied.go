package managedproxy

import "errors"

// Applied is the single source of truth for the private endpoint used by Cage.
func Applied(profile, endpoint, revision string) (Store, error) {
	p, err := Load(profile)
	if err != nil {
		return p, err
	}
	if _, ok := p.Find(p.Selected); !ok {
		return p, errors.New("尚未选择代理节点，请在网络与代理中添加来源并选择节点")
	}
	if revision == "" || p.Revision == "" || revision != p.Revision || endpoint != p.Endpoint() {
		return p, errors.New("代理节点尚未应用或配置不一致，请重新应用节点；外网保持关闭")
	}
	if _, err := p.Route(); err != nil {
		return p, err
	}
	return p, nil
}
