package server

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

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
) error {
	// 三个channel，分别用于：
	// 1. 读取视频帧
	frames := make(chan *media.Frame)
	// 2. 获取人脸识别结果
	faceResults := make(chan []reco.FaceInfo)
	// 3. 获取 GetCyclicFaceFeature 的错误
	sessionErr := make(chan error, 1)

	go func() {
		sessionErr <- session.GetCyclicFaceFeature(
			ctx,
			frames,
			faceResults,
			cyclicConfig,
		)
	}()

	// map[string]FaceInfo 用于缓存已经识别过的 embedding
	//
	// 这个精确 embedding 以前处理过没有？
	//              │
	//       ┌──────┴──────┐
	//       │             │
	//      没有           有
	//       │             │
	// 数据库余弦搜索     直接复用身份
	//       │
	//      Sign
	//       │
	//    保存结果
	MapServiceFaceInfos := make(map[string]ServiceFaceInfo)

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

		// 要发送的结果
		ServerResult := Result{
			Frame: frame,
			Faces: make([]ServiceFaceInfo, 0, len(newRecoFaceInfos)),
		}

		for i := range newRecoFaceInfos {
			if err := ctx.Err(); err != nil {
				return err
			}

			newFaceInfo := &newRecoFaceInfos[i]

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

			if len(newFaceInfo.Feature) != 0 {
				// 把 []float32 的原始二进制内容转换成 string，作为 map key
				buf := make([]byte, len(newFaceInfo.Feature)*4)

				for i, value := range newFaceInfo.Feature {
					binary.LittleEndian.PutUint32(
						buf[i*4:],
						math.Float32bits(value),
					)
				}

				// 将 buf 转换为 string，作为 map key
				key := string(buf)

				if oldServiceFaceInfo, ok := MapServiceFaceInfos[key]; ok {
					// 相同 embedding 已经处理过，直接复用身份信息
					newServiceFaceInfo.PersonID = oldServiceFaceInfo.PersonID
					newServiceFaceInfo.Name = oldServiceFaceInfo.Name
				} else {
					// 第一次看到这个 embedding，进行数据库余弦搜索
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

						// 每一个新的 embedding 都允许签到
						if err := db.Sign(person.PersonID); err != nil {
							return fmt.Errorf(
								"签到失败: %w",
								err,
							)
						}
					}

					// 无论是否匹配成功都缓存
					MapServiceFaceInfos[key] = newServiceFaceInfo
				}
			}

			ServerResult.Faces = append(ServerResult.Faces, newServiceFaceInfo)
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
