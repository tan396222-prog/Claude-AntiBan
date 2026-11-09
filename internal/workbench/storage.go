package workbench

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/containerenv"
)

func (s *Server) localVMStorage() bool {
	return !s.clientOnly && !containerenv.Configured(s.profile)
}

func (s *Server) environmentStorage(w http.ResponseWriter, r *http.Request) {
	if !s.localVMStorage() {
		http.Error(w, "仅本机虚拟机支持调整存储位置", http.StatusConflict)
		return
	}
	if r.Method == http.MethodGet {
		location, err := cage.GetStorageLocation(s.profile)
		if err != nil {
			requestError(w, err)
			return
		}
		jsonReply(w, location)
		return
	}

	var input struct {
		Directory string `json:"directory"`
		Previous  string `json:"previous"`
		Migrate   bool   `json:"migrate"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	s.launch(w, "storage", func() error {
		current, err := cage.GetStorageLocation(s.profile)
		if err != nil {
			return err
		}
		if current.Directory != input.Previous || current.HasEnvironment != input.Migrate {
			return errors.New("环境或存储位置已改变，请重新打开设置后再选择")
		}
		if err = cage.ValidateStorageTarget(s.profile, input.Directory); err != nil {
			return err
		}
		if err = s.validateStoragePlacement(input.Directory); err != nil {
			return err
		}
		if current.HasEnvironment {
			if err = s.stopEnvironment(); err != nil {
				return err
			}
		}
		s.setMessage("正在检查环境存储…")
		_, err = cage.MoveStorage(s.operationContext(), s.profile, input.Directory, current, func(copied, total int64) {
			s.setMessage(fmt.Sprintf("正在迁移环境数据 · %.1f / %.1f GiB", float64(copied)/(1<<30), float64(total)/(1<<30)))
		})
		return err
	})
}
