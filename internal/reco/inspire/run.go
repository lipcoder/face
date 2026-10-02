package inspire

import (
	"context"

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
