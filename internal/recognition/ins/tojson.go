package ins

import (
	"encoding/json"
	"fmt"
)

type FeatureFlags struct {
	Emotion     bool `json:"emotion,omitempty"`
	Liveness    bool `json:"liveness,omitempty"`
	Pose        bool `json:"pose,omitempty"`
	Quality     bool `json:"quality,omitempty"`
	Recognition bool `json:"recognition,omitempty"`
}

type APIError struct {
	Code            string `json:"code,omitempty"`
	Message         string `json:"message,omitempty"`
	InspireFaceCode int64  `json:"inspireface_code,omitempty"`
}

type HealthResponse struct {
	OK                         bool         `json:"ok"`
	Service                    string       `json:"service,omitempty"`
	SessionCount               int          `json:"session_count,omitempty"`
	FeatureLength              int          `json:"feature_length,omitempty"`
	RecommendedCosineThreshold float64      `json:"recommended_cosine_threshold,omitempty"`
	RequestedFeatures          FeatureFlags `json:"requested_features,omitempty"`
	ActiveFeatures             FeatureFlags `json:"active_features,omitempty"`
	Warnings                   []string     `json:"warnings,omitempty"`
	Error                      *APIError    `json:"error,omitempty"`
}

type MetadataResponse struct {
	OK                         bool      `json:"ok"`
	Endpoints                  []string  `json:"endpoints,omitempty"`
	FeatureLength              int       `json:"feature_length,omitempty"`
	RecommendedCosineThreshold float64   `json:"recommended_cosine_threshold,omitempty"`
	Error                      *APIError `json:"error,omitempty"`
}

type ImageInfo struct {
	Channels int `json:"channels,omitempty"`
	Height   int `json:"height,omitempty"`
	Width    int `json:"width,omitempty"`
}

type PoseInfo struct {
	Pitch float64 `json:"pitch,omitempty"`
	Roll  float64 `json:"roll,omitempty"`
	Yaw   float64 `json:"yaw,omitempty"`
}

type EmotionInfo struct {
	ID    int    `json:"id,omitempty"`
	Label string `json:"label,omitempty"`
}

type EmbeddingResult struct {
	Dim    int       `json:"dim,omitempty"`
	Vector []float64 `json:"vector,omitempty"`
}

type FaceResult struct {
	Index        int          `json:"index"`
	Box          []int        `json:"box,omitempty"`
	DetScore     float64      `json:"det_score,omitempty"`
	Embedding    []float64    `json:"embedding,omitempty"`
	EmbeddingDim int          `json:"embedding_dim,omitempty"`
	Pose         PoseInfo     `json:"pose,omitempty"`
	Quality      float64      `json:"quality,omitempty"`
	RGBLiveness  float64      `json:"rgb_liveness,omitempty"`
	Emotion      *EmotionInfo `json:"emotion,omitempty"`
}

type PhotoResponse struct {
	OK            bool         `json:"ok"`
	Endpoint      string       `json:"endpoint,omitempty"`
	Image         ImageInfo    `json:"image,omitempty"`
	FaceCount     int          `json:"face_count"`
	Faces         []FaceResult `json:"faces,omitempty"`
	FeatureLength int          `json:"feature_length,omitempty"`
	Error         *APIError    `json:"error,omitempty"`
}

type LivenessStats struct {
	Avg            float64 `json:"avg,omitempty"`
	Min            float64 `json:"min,omitempty"`
	Score          float64 `json:"score,omitempty"`
	Threshold      float64 `json:"threshold,omitempty"`
	PassFrameCount int     `json:"pass_frame_count,omitempty"`
	PassRatio      float64 `json:"pass_ratio,omitempty"`
	RequiredRatio  float64 `json:"required_ratio,omitempty"`
}

type QualityStats struct {
	Avg            float64 `json:"avg,omitempty"`
	Best           float64 `json:"best,omitempty"`
	Threshold      float64 `json:"threshold,omitempty"`
	PassFrameCount int     `json:"pass_frame_count,omitempty"`
}

type VideoFrameResult struct {
	Index       int      `json:"index"`
	FaceCount   int      `json:"face_count,omitempty"`
	Box         []int    `json:"box,omitempty"`
	DetScore    float64  `json:"det_score,omitempty"`
	Pose        PoseInfo `json:"pose,omitempty"`
	Quality     float64  `json:"quality,omitempty"`
	RGBLiveness float64  `json:"rgb_liveness,omitempty"`
	Score       float64  `json:"score,omitempty"`
}

type SelectedFrame struct {
	Index       int     `json:"index"`
	Quality     float64 `json:"quality,omitempty"`
	RGBLiveness float64 `json:"rgb_liveness,omitempty"`
	Score       float64 `json:"score,omitempty"`
}

type VideoLivenessEmbeddingResponse struct {
	OK             bool               `json:"ok"`
	Endpoint       string             `json:"endpoint,omitempty"`
	FrameCount     int                `json:"frame_count"`
	Live           bool               `json:"live"`
	Rule           string             `json:"rule,omitempty"`
	Quality        QualityStats       `json:"quality,omitempty"`
	Liveness       LivenessStats      `json:"liveness,omitempty"`
	BestFrame      *VideoFrameResult  `json:"best_frame,omitempty"`
	Embedding      *EmbeddingResult   `json:"embedding,omitempty"`
	SelectedFrames []SelectedFrame    `json:"selected_frames,omitempty"`
	Frames         []VideoFrameResult `json:"frames,omitempty"`
	Error          *APIError          `json:"error,omitempty"`
}

func BytesFromHealth(respBody []byte) (*HealthResponse, error) {
	var result HealthResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse health response failed: %w", err)
	}
	return &result, nil
}

func BytesFromMetadata(respBody []byte) (*MetadataResponse, error) {
	var result MetadataResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse metadata response failed: %w", err)
	}
	return &result, nil
}

func BytesFromPhoto(respBody []byte) (*PhotoResponse, error) {
	var result PhotoResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse photo response failed: %w", err)
	}
	return &result, nil
}

func BytesFromPhotoBasic(respBody []byte) (*PhotoResponse, error) {
	return BytesFromPhoto(respBody)
}

func BytesFromPhotoEmotion(respBody []byte) (*PhotoResponse, error) {
	return BytesFromPhoto(respBody)
}

func BytesFromPhotoPose(respBody []byte) (*PhotoResponse, error) {
	return BytesFromPhoto(respBody)
}

func BytesFromVideoLivenessEmbedding(respBody []byte) (*VideoLivenessEmbeddingResponse, error) {
	var result VideoLivenessEmbeddingResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse video liveness embedding response failed: %w", err)
	}
	return &result, nil
}

func (r *PhotoResponse) FirstFace() *FaceResult {
	if r == nil || len(r.Faces) == 0 {
		return nil
	}
	return &r.Faces[0]
}

func (r *VideoLivenessEmbeddingResponse) HasEmbedding() bool {
	return r != nil &&
		r.Embedding != nil &&
		r.Embedding.Dim > 0 &&
		len(r.Embedding.Vector) == r.Embedding.Dim
}
