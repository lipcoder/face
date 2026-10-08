package inspire

import (
	"context"
	"fmt"
	"math"

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

func (s *Session) GetCyclicFaceFeature(
	ctx context.Context,
	frames <-chan *media.Frame,
	results chan<- []reco.FaceInfo,
	retryTracks <-chan []int64,
	config reco.CyclicFeatureConfig,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.closed {
		return fmt.Errorf("session已关闭")
	}
	if frames == nil || results == nil || retryTracks == nil {
		return fmt.Errorf("帧流或结果流为空")
	}

	// readyFrames：每条轨迹连续满足位置和姿态要求的帧数
	readyFrames := make(map[int64]int)
	// 匹配成功后保留首次特征，直到轨迹离开；匹配失败后删除并重新提取。
	readyFaceInfos := make(map[int64]reco.FaceInfo)
	needFeature := true

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame, ok := <-frames:
			if !ok {
				return nil
			}

			var newFaceInfos []reco.FaceInfo
			var err error
			if needFeature {
				newFaceInfos, err = s.GetFaceFeature(ctx, frame)
			} else {
				newFaceInfos, err = s.GetFacePlace(ctx, frame)
			}
			if err != nil {
				return err
			}

			// seeFaceInfos 记录本帧出现的 TrackID，用于删除已经离开画面的轨迹状态
			seeFaceInfos := make(map[int64]bool, len(newFaceInfos))

			for i := range newFaceInfos {

				faceInfo := &newFaceInfos[i]
				seeFaceInfos[faceInfo.TrackID] = true
				if cached, ok := readyFaceInfos[faceInfo.TrackID]; ok {
					faceInfo.Feature = cached.Feature
					faceInfo.Quality = cached.Quality
					continue
				}

				// 检查人脸是否满足提取特征的条件
				if !faceReadyForRecognition(*faceInfo, frame, config) {
					delete(readyFrames, faceInfo.TrackID) // 如果不满足条件，则删除该轨迹的连续帧计数
					faceInfo.Feature = nil
					faceInfo.Quality = 0
					continue // 如果不满足条件，则跳过该人脸
				}
				// 如果该轨迹已经连续满足条件的帧数小于最小要求，则增加计数
				if readyFrames[faceInfo.TrackID] < config.MinReadyFrames {
					readyFrames[faceInfo.TrackID]++
				}

				// 连续帧数、质量或特征不合格时，本帧只展示位置。
				if readyFrames[faceInfo.TrackID] < config.MinReadyFrames ||
					(s.enableQuality && !(faceInfo.Quality >= config.MinSearchFaceQuality)) || len(faceInfo.Feature) == 0 || len(faceInfo.Feature) != FeatureLength {
					faceInfo.Feature = nil
					faceInfo.Quality = 0
					continue // 如果不满足条件，则跳过该人脸
				}
				readyFaceInfos[faceInfo.TrackID] = *faceInfo
			}

			// 删除已经离开画面的轨迹状态
			for id := range readyFrames {
				if !seeFaceInfos[id] {
					delete(readyFrames, id) // 如果该轨迹在本帧未出现，则删除其连续帧计数
				}
			}
			// 删除已经离开画面的轨迹状态
			for id := range readyFaceInfos {
				if !seeFaceInfos[id] {
					delete(readyFaceInfos, id) // 如果该轨迹在本帧未出现，则删除其缓存的人脸信息
				}
			}

			select {
			case results <- newFaceInfos:
			case <-ctx.Done():
				return ctx.Err()
			}
			// 等待本帧匹配结果，只有失败的轨迹在下一帧重新提取特征。
			select {
			case tracks, ok := <-retryTracks:
				if !ok {
					return nil
				}
				for _, id := range tracks {
					delete(readyFaceInfos, id)
				}
				needFeature = len(tracks) > 0 || len(newFaceInfos) == 0
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// faceReadyForRecognition 判断人脸是否满足提取特征的条件
func faceReadyForRecognition(face reco.FaceInfo, frame *media.Frame, config reco.CyclicFeatureConfig) bool {
	if frame == nil || face.Box.Width <= 0 || face.Box.Height <= 0 {
		return false
	}
	marginX := int(math.Ceil(float64(face.Box.Width) * config.FaceEdgeMargin))
	marginY := int(math.Ceil(float64(face.Box.Height) * config.FaceEdgeMargin))
	return face.Box.X >= marginX && face.Box.Y >= marginY &&
		face.Box.X+face.Box.Width+marginX <= frame.Width &&
		face.Box.Y+face.Box.Height+marginY <= frame.Height &&
		math.Abs(float64(face.Angles.Yaw)) <= 30 &&
		math.Abs(float64(face.Angles.Pitch)) <= 25 &&
		math.Abs(float64(face.Angles.Roll)) <= 30
}
