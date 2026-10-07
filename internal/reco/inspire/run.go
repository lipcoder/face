package inspire

import (
	"context"
	"fmt"
	"math"

	"github.com/lipcoder/face/internal/media"
	"github.com/lipcoder/face/internal/reco"
)

// const (
// 	minReadyFrames       = 3    // 连续满足条件的最少帧数
// 	minSearchFaceQuality = 0.6  // 人脸质量的最小阈值，低于此值的人脸不参与特征提取
// 	faceEdgeMargin       = 0.05 // 人脸框四周至少留出自身边长 5% 的画面空间
// )

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
	config reco.CyclicFeatureConfig,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.closed {
		return fmt.Errorf("session已关闭")
	}
	if frames == nil || results == nil {
		return fmt.Errorf("帧流或结果流为空")
	}

	// readyFaceInfos：已通过筛选的轨迹及其特征，键是 TrackID
	readyFaceInfos := make(map[int64][]reco.FaceInfo)
	// readyFrames：每条轨迹连续满足位置和姿态要求的帧数
	readyFrames := make(map[int64]int)

	needFeature := false

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame, ok := <-frames:
			if !ok {
				return nil
			}

			// 当前循环内帧的处理方式
			thisNeedFeature := needFeature
			var newFaceInfos []reco.FaceInfo
			var err error

			// 检测阶段只取位置；连续两帧合格后，下一帧再提取特征
			if thisNeedFeature {
				newFaceInfos, err = s.GetFaceFeature(ctx, frame)
			} else {
				newFaceInfos, err = s.GetFacePlace(ctx, frame)
			}
			if err != nil {
				return err
			}

			// seeFaceInfos 记录本帧出现的 TrackID，用于删除已经离开画面的轨迹状态
			seeFaceInfos := make(map[int64]bool, len(newFaceInfos))
			needFeature = false

			for i := range newFaceInfos {

				faceInfo := &newFaceInfos[i]
				seeFaceInfos[faceInfo.TrackID] = true

				// 检查人脸是否满足提取特征的条件
				if !faceReadyForRecognition(*faceInfo, frame, config) {
					delete(readyFrames, faceInfo.TrackID) // 如果不满足条件，则删除该轨迹的连续帧计数
					faceInfo.Feature = nil
					faceInfo.Quality = 0
					continue // 如果不满足条件，则跳过该人脸
				}

				// 如果轨迹人脸已在缓存中，则直接使用缓存的特征和质量
				if cached, ok := readyFaceInfos[faceInfo.TrackID]; ok && len(cached) > 0 {
					faceInfo.Feature = cached[0].Feature
					faceInfo.Quality = cached[0].Quality
					continue // 如果已缓存，则跳过该人脸
				}

				// 如果该轨迹已经连续满足条件的帧数小于最小要求，则增加计数
				if readyFrames[faceInfo.TrackID] < config.MinReadyFrames {
					readyFrames[faceInfo.TrackID]++
				}

				// 如果当前轨迹连续帧还未达要求，或者当前帧不需要提取特征，或者质量不达标，则跳过该人脸
				// 但如果当前轨迹连续帧数已经达到要求，则下一帧需要提取特征
				if readyFrames[faceInfo.TrackID] < config.MinReadyFrames || !thisNeedFeature ||
					(s.enableQuality && !(faceInfo.Quality >= config.MinSearchFaceQuality)) || len(faceInfo.Feature) == 0 || len(faceInfo.Feature) != FeatureLength {
					faceInfo.Feature = nil
					faceInfo.Quality = 0
					if readyFrames[faceInfo.TrackID] >= config.MinReadyFrames-1 {
						needFeature = true
					}
					continue // 如果不满足条件，则跳过该人脸
				}

				// 如果当前轨迹连续帧数已经达到要求，则将该人脸信息缓存起来
				readyFaceInfos[faceInfo.TrackID] = append(readyFaceInfos[faceInfo.TrackID], *faceInfo)

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
