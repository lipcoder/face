package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/lipcoder/face/internal/database"
	"github.com/lipcoder/face/internal/media"
	"github.com/lipcoder/face/internal/reco"
	"github.com/lipcoder/face/internal/source"
)

type Box struct {
	X      float32
	Y      float32
	Width  float32
	Height float32
}

type ServiceFaceInfo struct {
	TrackID  int64
	PersonID string
	Name     string
	Box      Box
}

type Result struct {
	Frame *media.Frame
	Faces []ServiceFaceInfo
}

func Run(
	ctx context.Context,
	db database.Database,
	session reco.Session,
	src source.Source,
	output chan Result,
	cyclicConfig reco.CyclicFeatureConfig,
	refresh <-chan struct{},
) error {
	// 输入帧、检测结果和未匹配轨迹在两个循环之间同步传递。
	frames := make(chan *media.Frame)
	defer close(frames)
	faceResults := make(chan []reco.FaceInfo)
	retryTracks := make(chan []int64)
	defer close(retryTracks)
	// 接收检测循环的结束或错误。
	sessionErr := make(chan error, 1)

	go func() {
		sessionErr <- session.GetCyclicFaceFeature(
			ctx,
			frames,
			faceResults,
			retryTracks,
			cyclicConfig,
		)
	}()

	// 只缓存已匹配的轨迹身份；未匹配的轨迹继续使用后续帧重新查询。
	MapServiceFaceInfos := make(map[int64]ServiceFaceInfo)

	for {
		frame, err := src.Read(ctx)
		if err != nil {
			return err
		}
		if frame == nil {
			continue
		}

		// 将一帧发送给 GetCyclicFaceFeature
		select {
		case frames <- frame:
		case err := <-sessionErr:
			if err != nil {
				return err
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}

		// 等待这一帧对应的人脸结果
		var newRecoFaceInfos []reco.FaceInfo

		select {
		case newRecoFaceInfos = <-faceResults:
		case err := <-sessionErr:
			if err != nil {
				return err
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
		// 有刷新请求时，在匹配本帧身份前清空缓存。
		select {
		case <-refresh:
			clear(MapServiceFaceInfos)
		default:
		}

		// 要发送的结果
		// 展示端异步消费结果，保留画面副本，避免视频源下一次 Read 复用缓冲区
		resultFrame := *frame
		resultFrame.Data = append([]byte(nil), frame.Data...)
		ServerResult := Result{
			Frame: &resultFrame,
			Faces: make([]ServiceFaceInfo, 0, len(newRecoFaceInfos)),
		}

		seenTracks := make(map[int64]bool, len(newRecoFaceInfos))
		var unmatchedTracks []int64
		for i := range newRecoFaceInfos {
			if err := ctx.Err(); err != nil {
				return err
			}

			newFaceInfo := &newRecoFaceInfos[i]
			seenTracks[newFaceInfo.TrackID] = true

			// 将 reco.FaceInfo 转换为 ServiceFaceInfo
			newServiceFaceInfo := ServiceFaceInfo{
				TrackID: newFaceInfo.TrackID,
				Box: Box{
					X:      float32(newFaceInfo.Box.X) / float32(frame.Width),
					Y:      float32(newFaceInfo.Box.Y) / float32(frame.Height),
					Width:  float32(newFaceInfo.Box.Width) / float32(frame.Width),
					Height: float32(newFaceInfo.Box.Height) / float32(frame.Height),
				},
			}

			if oldServiceFaceInfo, ok := MapServiceFaceInfos[newFaceInfo.TrackID]; ok {
				newServiceFaceInfo.PersonID = oldServiceFaceInfo.PersonID
				newServiceFaceInfo.Name = oldServiceFaceInfo.Name
			} else if len(newFaceInfo.Feature) != 0 {
				person, found, err := db.SearchByFeature(newFaceInfo.Feature)
				if err != nil {
					return fmt.Errorf(
						"查询人脸特征失败: %w",
						err,
					)
				}

				if found {
					newServiceFaceInfo.PersonID = person.PersonID
					newServiceFaceInfo.Name = person.Name

					// 轨迹首次匹配成功时记录签到。
					signed, err := db.Sign(person.PersonID)
					if err != nil {
						return fmt.Errorf(
							"签到失败: %w",
							err,
						)
					}
					if !signed {
						found = false
						newServiceFaceInfo.PersonID = ""
						newServiceFaceInfo.Name = ""
					}
				}

				// 未匹配时不缓存，下一帧使用新特征继续识别。
				if found {
					MapServiceFaceInfos[newFaceInfo.TrackID] = newServiceFaceInfo
				}
			}

			if newServiceFaceInfo.PersonID == "" {
				unmatchedTracks = append(unmatchedTracks, newFaceInfo.TrackID)
			}
			ServerResult.Faces = append(ServerResult.Faces, newServiceFaceInfo)
		}
		for id := range MapServiceFaceInfos {
			if !seenTracks[id] {
				delete(MapServiceFaceInfos, id)
			}
		}
		select {
		case retryTracks <- unmatchedTracks:
		case err := <-sessionErr:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}

		// 发布最新结果
		select {
		case output <- ServerResult:
			continue
		default:
		}

		select {
		case <-output:
		default:
		}

		select {
		case output <- ServerResult:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func Add(
	ctx context.Context,
	db database.Database,
	session reco.Session,
	src source.Source,
	personID string,
	name string,
	multiFrameConfig reco.MultiFrameFeatureConfig,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("session为空")
	}
	if src == nil {
		return fmt.Errorf("视频源为空")
	}
	if personID == "" || name == "" {
		return fmt.Errorf("personID或name为空")
	}

	config := multiFrameConfig

	// context.WithCancel(ctx)创建一个可单独停止的子 context，
	// 传给 Add 的读帧协程和识别函数。识别完成或报错后调用 cancel()，
	// 让还在等待下一帧或 status的协程退出，这样 Add才能等它结束。
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	frames := make(chan *media.Frame) // 读取视频帧的 channel
	status := make(chan bool)         // 处理状态的 channel
	readDone := make(chan error, 1)   // 读取视频帧的协程完成后会发送一个错误或nil到这个 channel
	go func() {
		defer close(frames)
		for sent := 0; sent < config.MaxFrames; {
			frame, err := src.Read(readCtx)
			if err != nil {
				readDone <- err
				return
			}
			if frame == nil {
				continue
			}
			select {
			case frames <- frame:
			case <-readCtx.Done():
				readDone <- readCtx.Err()
				return
			}
			select {
			case ready := <-status:
				if !ready {
					readDone <- fmt.Errorf("人脸帧处理失败")
					return
				}
				sent++
			case <-readCtx.Done():
				readDone <- readCtx.Err()
				return
			}
		}
		readDone <- nil
	}()

	feature, err := session.GetFaceMultiFeature(readCtx, frames, status, config)
	cancel()
	sourceErr := <-readDone
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if sourceErr != nil && !errors.Is(sourceErr, context.Canceled) {
			return fmt.Errorf("读取视频帧失败: %w", sourceErr)
		}
		return fmt.Errorf("提取人脸特征失败: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// 将提取到的人脸特征保存到数据库
	if err := db.AddPerson(&database.Person{
		PersonID: personID,
		Name:     name,
		Feature:  feature,
	}); err != nil {
		return fmt.Errorf("添加人员失败: %w", err)
	}
	return nil
}
