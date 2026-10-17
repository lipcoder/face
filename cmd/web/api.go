package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type Operation string

const (
	ListPeople         Operation = "list_people"
	CreatePerson       Operation = "create_person"
	RenamePerson       Operation = "rename_person"
	DeletePerson       Operation = "delete_person"
	ListAttendance     Operation = "list_attendance"
	ListWeekAttendance Operation = "list_week_attendance"
	GetStatus          Operation = "get_status"
	RefreshRecognition Operation = "refresh_recognition"
)

// Request 由 Web 解析后交给 main 执行；Reply 只返回该请求的结果。
type Request struct {
	Context   context.Context
	Operation Operation
	PersonID  string
	Name      string
	Date      string
	Reply     chan Response
}

type Response struct {
	Status int
	Data   any
	Err    error
}

func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request, operation Operation, personID, name, date string) {
	if h.requests == nil {
		http.Error(w, "服务未就绪", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	if operation == CreatePerson {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	request := Request{Context: ctx, Operation: operation, PersonID: personID, Name: name, Date: date, Reply: make(chan Response, 1)}
	select {
	case h.requests <- request:
	case <-h.done:
		http.Error(w, "服务已停止", http.StatusServiceUnavailable)
		return
	case <-ctx.Done():
		http.Error(w, ctx.Err().Error(), http.StatusRequestTimeout)
		return
	}
	select {
	case response := <-request.Reply:
		if response.Err != nil {
			status := response.Status
			if status == 0 {
				status = http.StatusInternalServerError
			}
			http.Error(w, response.Err.Error(), status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if response.Status != 0 {
			w.WriteHeader(response.Status)
		}
		if response.Status != http.StatusNoContent {
			_ = json.NewEncoder(w).Encode(response.Data)
		}
	case <-ctx.Done():
		http.Error(w, ctx.Err().Error(), http.StatusRequestTimeout)
	case <-h.done:
		http.Error(w, "服务已停止", http.StatusServiceUnavailable)
	}
}

func (h *Handler) handlePeople(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.dispatch(w, r, ListPeople, "", "", "")
	case http.MethodPost:
		id, name, err := parsePerson(r, w)
		if err != nil || !validID(id) || name == "" {
			http.Error(w, "请输入 10 位数字学号和姓名", http.StatusBadRequest)
			return
		}
		h.dispatch(w, r, CreatePerson, id, name, "")
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handlePerson(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/people/")
	if !validID(id) {
		http.Error(w, "无效的学号", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		_, name, err := parsePerson(r, w)
		if err != nil || name == "" {
			http.Error(w, "姓名不能为空", http.StatusBadRequest)
			return
		}
		h.dispatch(w, r, RenamePerson, id, name, "")
	case http.MethodDelete:
		h.dispatch(w, r, DeletePerson, id, "", "")
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleAttendance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, date := r.URL.Query().Get("person_id"), r.URL.Query().Get("date")
	if r.URL.Query().Get("week") == "current" && id == "" && date == "" {
		h.dispatch(w, r, ListWeekAttendance, "", "", "")
		return
	}
	if id != "" && (!validID(id) || date != "") {
		http.Error(w, "只能按学号或日期查询", http.StatusBadRequest)
		return
	}
	h.dispatch(w, r, ListAttendance, id, "", date)
}

func parsePerson(r *http.Request, w http.ResponseWriter) (string, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var person struct {
		PersonID string `json:"person_id"`
		Name     string `json:"name"`
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&person); err != nil {
			return "", "", err
		}
	} else {
		if err := r.ParseForm(); err != nil {
			return "", "", err
		}
		person.PersonID, person.Name = r.FormValue("person_id"), r.FormValue("name")
	}
	return strings.TrimSpace(person.PersonID), strings.TrimSpace(person.Name), nil
}

func validID(id string) bool {
	if len(id) != 10 {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
