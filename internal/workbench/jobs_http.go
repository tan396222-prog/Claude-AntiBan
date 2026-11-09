package workbench

import (
	"errors"
	"net/http"
)

func (s *Server) launch(w http.ResponseWriter, kind string, run func() error) {
	job, err := s.submitJob(kind, run)
	switch {
	case errors.Is(err, errJobsClosed):
		http.Error(w, "后台正在更新，请重新打开 Cage。", http.StatusServiceUnavailable)
	case errors.Is(err, errJobBusy):
		http.Error(w, "另一个操作仍在进行，请稍候", http.StatusConflict)
	case err != nil:
		requestError(w, err)
	default:
		jsonReply(w, job)
	}
}
