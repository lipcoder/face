package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/lipcoder/face/cmd/web"
	"github.com/lipcoder/face/internal/database"
	"github.com/lipcoder/face/internal/reco"
	"github.com/lipcoder/face/internal/source"
	"github.com/lipcoder/face/server"
)

type personView struct {
	PersonID string `json:"person_id"`
	Name     string `json:"name"`
}

type attendanceView struct {
	PersonID string `json:"person_id"`
	SignDate string `json:"sign_date"`
	SignedAt string `json:"signed_at"`
}

type weekPersonView struct {
	PersonID string `json:"person_id"`
	Name     string `json:"name"`
	SignDate string `json:"sign_date"`
	SignedAt string `json:"signed_at"`
}

func serveCommands(ctx context.Context, requests <-chan web.Request, db database.Database,
	enroller reco.Session, enrollmentFrames source.Source, refresh chan<- struct{},
	sourceName string, width, height, fps int,
) {
	busy := make(chan struct{}, 1)
	var enrollments sync.WaitGroup
	defer enrollments.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-requests:
			if req.Context.Err() != nil {
				continue
			}
			if req.Operation == web.CreatePerson {
				_, found, err := db.SearchPerson(&database.Person{PersonID: req.PersonID})
				if err != nil {
					req.Reply <- web.Response{Err: err}
					continue
				}
				if found {
					req.Reply <- web.Response{Status: http.StatusConflict, Err: errors.New("学号已存在")}
					continue
				}
				select {
				case busy <- struct{}{}:
					enrollments.Add(1)
					go func() {
						defer enrollments.Done()
						defer func() { <-busy }()
						enrollCtx, cancel := context.WithCancel(req.Context)
						stopCancel := context.AfterFunc(ctx, cancel)
						defer stopCancel()
						defer cancel()
						err := server.Add(enrollCtx, db, enroller, enrollmentFrames, req.PersonID, req.Name,
							reco.MultiFrameFeatureConfig{SampleCount: 5, MaxFrames: 150, MinQuality: 0.5})
						if err != nil {
							req.Reply <- web.Response{Status: http.StatusUnprocessableEntity, Err: err}
							return
						}
						requestRefresh(refresh)
						req.Reply <- web.Response{Status: http.StatusCreated, Data: personView{PersonID: req.PersonID, Name: req.Name}}
					}()
				default:
					req.Reply <- web.Response{Status: http.StatusConflict, Err: errors.New("正在录入另一人")}
				}
				continue
			}
			req.Reply <- executeCommand(ctx, req, db, refresh, sourceName, width, height, fps)
		}
	}
}

func executeCommand(ctx context.Context, req web.Request, db database.Database, refresh chan<- struct{},
	sourceName string, width, height, fps int,
) web.Response {
	switch req.Operation {
	case web.ListPeople:
		people, err := db.ListPersons()
		if err != nil {
			return web.Response{Err: err}
		}
		result := make([]personView, 0, len(people))
		for _, person := range people {
			result = append(result, personView{PersonID: person.PersonID, Name: person.Name})
		}
		return web.Response{Data: result}
	case web.RenamePerson:
		found, err := db.UpdatePersonName(req.PersonID, req.Name)
		if err != nil {
			return web.Response{Err: err}
		}
		if !found {
			return web.Response{Status: http.StatusNotFound, Err: errors.New("人员不存在")}
		}
		requestRefresh(refresh)
		return web.Response{Data: personView{PersonID: req.PersonID, Name: req.Name}}
	case web.DeletePerson:
		found, err := db.DeletePerson(req.PersonID)
		if err != nil {
			return web.Response{Err: err}
		}
		if !found {
			return web.Response{Status: http.StatusNotFound, Err: errors.New("人员不存在")}
		}
		requestRefresh(refresh)
		return web.Response{Status: http.StatusNoContent}
	case web.RefreshRecognition:
		requestRefresh(refresh)
		return web.Response{Status: http.StatusAccepted, Data: map[string]bool{"queued": true}}
	case web.ListAttendance:
		var records []database.Attendance
		var err error
		if req.PersonID != "" {
			records, err = db.GetAttendanceByPerson(req.PersonID)
		} else {
			date := req.Date
			if date == "" {
				date = time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
			}
			records, err = db.GetAttendanceByDate(date)
		}
		if err != nil {
			return web.Response{Status: http.StatusBadRequest, Err: err}
		}
		result := make([]attendanceView, 0, len(records))
		for _, record := range records {
			result = append(result, attendanceView{PersonID: record.PersonID, SignDate: record.SignDate, SignedAt: record.SignedAt})
		}
		return web.Response{Data: result}
	case web.ListWeekAttendance:
		now := time.Now().In(time.FixedZone("CST", 8*3600))
		monday := now.AddDate(0, 0, -(int(now.Weekday())+6)%7)
		records, err := db.GetAttendanceByRange(monday.Format("2006-01-02"), monday.AddDate(0, 0, 6).Format("2006-01-02"))
		if err != nil {
			return web.Response{Err: err}
		}
		people, err := db.ListPersons()
		if err != nil {
			return web.Response{Err: err}
		}
		names := make(map[string]string, len(people))
		for _, person := range people {
			names[person.PersonID] = person.Name
		}
		roster := make([]personView, 0, len(people))
		for _, person := range people {
			roster = append(roster, personView{PersonID: person.PersonID, Name: person.Name})
		}
		result := make([]weekPersonView, 0, len(records))
		for _, record := range records {
			result = append(result, weekPersonView{PersonID: record.PersonID, Name: names[record.PersonID], SignDate: record.SignDate, SignedAt: record.SignedAt})
		}
		return web.Response{Data: struct {
			WeekStart string           `json:"week_start"`
			People    []personView     `json:"people"`
			Records   []weekPersonView `json:"records"`
		}{WeekStart: monday.Format("2006-01-02"), People: roster, Records: result}}
	case web.GetStatus:
		people, err := db.ListPersons()
		if err != nil {
			return web.Response{Err: err}
		}
		return web.Response{Data: struct {
			Running     bool    `json:"running"`
			Source      string  `json:"source"`
			SourceReady bool    `json:"source_ready"`
			Width       int     `json:"width"`
			Height      int     `json:"height"`
			Framerate   float64 `json:"framerate"`
			People      int     `json:"people"`
		}{Running: ctx.Err() == nil, Source: sourceName, SourceReady: true, Width: width, Height: height, Framerate: float64(fps), People: len(people)}}
	default:
		return web.Response{Status: http.StatusBadRequest, Err: errors.New("未知请求")}
	}
}

func requestRefresh(refresh chan<- struct{}) {
	select {
	case refresh <- struct{}{}:
	default:
	}
}
