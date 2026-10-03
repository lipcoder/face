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

func (s *Session) GetCyclicFaceFeature(ctx context.Context, frames <-chan *media.Frame, results chan<- []reco.FaceInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.closed {
		return fmt.Errorf("session已关闭")
	}
	if frames == nil || results == nil {
		return fmt.Errorf("帧流或结果流为空")
	}

	oldFaceInfos := make(map[int64]reco.FaceInfo)
	needFeature := true

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame, ok := <-frames:
			if !ok {
				return nil
			}

			// 如果需要特征，则获取人脸特征
			if needFeature {
				newFaceInfos, err := s.GetFaceFeature(ctx, frame)
				if err != nil {
					return err
				}
				// 当前无人脸，返回结果
				if len(newFaceInfos) == 0 {
					clear(oldFaceInfos)
					needFeature = true

					results <- newFaceInfos
					continue
				}

				for i := range newFaceInfos {
					trackid := newFaceInfos[i].TrackID

					if faceinfo, ok := oldFaceInfos[trackid]; ok {
						// 这是以前已经识别过的 Track，继续使用以前保存的 embedding
						newFaceInfos[i].Feature = faceinfo.Feature
						newFaceInfos[i].Quality = faceinfo.Quality
						continue
					}
					// 新 Track，保存第一次得到的 embedding
					oldFaceInfos[trackid] = newFaceInfos[i]
				}

				needFeature = false
				select {
				case results <- newFaceInfos:
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			} else {
				newFaceInfos, err := s.GetFacePlace(ctx, frame)
				if err != nil {
					return err
				}
				// 当前无人脸，返回结果
				if len(newFaceInfos) == 0 {
					clear(oldFaceInfos)
					needFeature = true

					select {
					case results <- newFaceInfos:
					case <-ctx.Done():
						return ctx.Err()
					}
					continue
				}

				for i := range newFaceInfos {
					trackid := newFaceInfos[i].TrackID
					// 检查是否是以前已经识别过的 Track，如果是，则继续使用以前保存的 embedding
					if faceinfo, ok := oldFaceInfos[trackid]; ok {
						newFaceInfos[i].Feature = faceinfo.Feature
						newFaceInfos[i].Quality = faceinfo.Quality
					} else {
						needFeature = true
					}
				}

				select {
				case results <- newFaceInfos:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}
