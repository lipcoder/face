package inspire

import (
	"context"
	"fmt"

	"github.com/lipcoder/face/internal/media"
	"github.com/lipcoder/face/internal/reco"
)

func (s *Session) GetFacePlace(ctx context.Context, frame *media.Frame) ([]reco.FaceInfo, error) {
	faces, err := s.process(ctx, frame, false, false)
	if err != nil {
		return nil, err
	}

	facesInfo := make([]reco.FaceInfo, len(faces))
	for i, face := range faces {
		facesInfo[i] = reco.FaceInfo{
			TrackID:             face.TrackID,
			TrackCount:          face.TrackCount,
			Box:                 face.Box,
			Angles:              face.Angles,
			DetectionConfidence: face.DetectionConfidence,
		}
	}
	return facesInfo, nil
}

func (s *Session) GetFaceFeature(ctx context.Context, frame *media.Frame) ([]reco.FaceInfo, error) {
	faces, err := s.process(ctx, frame, true, true)
	if err != nil {
		return nil, err
	}

	facesInfo := make([]reco.FaceInfo, len(faces))
	for i, face := range faces {
		facesInfo[i] = reco.FaceInfo{
			TrackID:             face.TrackID,
			TrackCount:          face.TrackCount,
			Box:                 face.Box,
			Angles:              face.Angles,
			DetectionConfidence: face.DetectionConfidence,
			Quality:             face.Quality,
			Feature:             face.Feature,
		}
	}
	return facesInfo, nil
}

// GetCyclicFaceFeature 持续处理视频帧。
//
// 每个 TrackID 只向外返回一次 Feature。
// 后续帧只更新该 Track 的位置等跟踪信息。
//
// 当 GetFacePlace 发现新的 TrackID 时，本帧直接以 Feature=nil 返回；
// 下一帧切换到 GetFaceFeature，为新 Track 提取特征。
//
// 当画面中不存在任何人脸时，清空已有 Track 状态，并持续使用
// GetFaceFeature，保证下一批人脸出现的第一帧即可获得特征。
func (s *Session) GetCyclicFaceFeature(
	ctx context.Context,
	frames <-chan *media.Frame,
	results chan<- []reco.FaceInfo,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.closed {
		return fmt.Errorf("session已关闭")
	}
	if !s.enableRecognition {
		return fmt.Errorf("session未启用人脸特征提取")
	}
	if frames == nil || results == nil {
		return fmt.Errorf("帧流或结果流为空")
	}

	knownTracks := make(map[int64]struct{}) // 已经向上层交付过 Feature 的 TrackID
	needFeature := true                     // 启动第一帧需要完整提取 Feature

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case frame, ok := <-frames:
			if !ok {
				return nil
			}
			if frame == nil {
				return fmt.Errorf("帧数据为空")
			}

			var faces []reco.FaceInfo
			var err error

			if needFeature {
				faces, err = s.GetFaceFeature(ctx, frame)
			} else {
				faces, err = s.GetFacePlace(ctx, frame)
			}
			if err != nil {
				return err
			}

			// 出现空白帧，清空已知 TrackID，下一帧继续 GetFaceFeature
			if len(faces) == 0 {
				clear(knownTracks)
				needFeature = true

				results <- faces
				continue
			}

			if needFeature {
				// 当前帧已经执行了 GetFaceFeature，标记所有 TrackID 为已知
				for i := range faces {
					trackID := faces[i].TrackID

					if _, ok := knownTracks[trackID]; ok {
						faces[i].Feature = nil
						continue
					}

					knownTracks[trackID] = struct{}{}
				}

				// 当前可见的所有 Track 都已经处理过 Feature
				needFeature = false
			} else {
				// 当前帧执行了 GetFacePlace，检查是否有新的 TrackID 出现
				for _, face := range faces {
					if _, ok := knownTracks[face.TrackID]; !ok {
						needFeature = true
						break
					}
				}
			}

			select {
			case results <- faces:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
