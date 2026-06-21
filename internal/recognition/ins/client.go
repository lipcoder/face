package ins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"lipcoder/face/internal/recognition"
	"mime/multipart"
	"net/http"
	"strings"
)

const (
	endpointHealth                 = "/health"
	endpointMetadata               = "/metadata"
	endpointPhotoBasic             = "/photo/basic"
	endpointPhotoPose              = "/photo/pose"
	endpointPhotoEmotion           = "/photo/emotion"
	endpointVideoLivenessEmbedding = "/video/liveness-embedding"
)

type Inspire struct {
	client          *http.Client
	ctx             context.Context
	baseURL         string
	maxResponseSize int64
}

type Client interface {
	Health(ctx context.Context) (*HealthResponse, error)
	Metadata(ctx context.Context) (*MetadataResponse, error)
	AnalyzeBasic(ctx context.Context, imgBytes []byte) (*recognition.PhotoResult, error)
	AnalyzeState(ctx context.Context, imgBytes []byte) (*recognition.StateResult, error)
	AnalyzeEmotion(ctx context.Context, imgBytes []byte) (*recognition.EmotionResult, error)
	GetOneFaceEmbedding(ctx context.Context, imgBytes []byte) ([]float64, error)
	GetBestFaceEmbedding(ctx context.Context, imgBytes []byte) ([]float64, error)
	AnalyzeLivenessEmbedding(ctx context.Context, frames [][]byte) (*recognition.LivenessResult, error)
	GetLivenessEmbedding(ctx context.Context, frames [][]byte) ([]float64, error)
}

var (
	_ Client                  = (*Inspire)(nil)
	_ recognition.Recognition = (*Inspire)(nil)
	_ recognition.Analyzer    = (*Inspire)(nil)
)

func NewInspire(client *http.Client, ctx context.Context, baseURL string, maxResponseSize int64) (*Inspire, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if client == nil || ctx == nil || baseURL == "" || maxResponseSize <= 0 || client.Timeout <= 0 {
		return nil, recognition.ErrInvalidConfig
	}

	return &Inspire{
		client:          client,
		ctx:             ctx,
		baseURL:         baseURL,
		maxResponseSize: maxResponseSize,
	}, nil
}

func (a *Inspire) Health(ctx context.Context) (*HealthResponse, error) {
	respBody, err := a.get(ctx, endpointHealth)
	if err != nil {
		return nil, fmt.Errorf("inspire health: %w", err)
	}

	resp, err := BytesFromHealth(respBody)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", recognition.ErrInvalidResponse, err)
	}
	if !resp.OK {
		return nil, recognition.ErrRecognitionFailed
	}

	return resp, nil
}

func (a *Inspire) Metadata(ctx context.Context) (*MetadataResponse, error) {
	respBody, err := a.get(ctx, endpointMetadata)
	if err != nil {
		return nil, fmt.Errorf("inspire metadata: %w", err)
	}

	resp, err := BytesFromMetadata(respBody)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", recognition.ErrInvalidResponse, err)
	}
	if !resp.OK {
		return nil, recognition.ErrRecognitionFailed
	}

	return resp, nil
}

func (a *Inspire) AnalyzeBasic(ctx context.Context, imgBytes []byte) (*recognition.PhotoResult, error) {
	resp, err := a.analyzePhoto(ctx, endpointPhotoBasic, imgBytes)
	if err != nil {
		return nil, err
	}

	return photoResult(resp), nil
}

func (a *Inspire) AnalyzeState(ctx context.Context, imgBytes []byte) (*recognition.StateResult, error) {
	resp, err := a.analyzePhoto(ctx, endpointPhotoPose, imgBytes)
	if err != nil {
		return nil, err
	}

	result := photoResult(resp)
	return &recognition.StateResult{
		Image:     result.Image,
		FaceCount: result.FaceCount,
		Faces:     result.Faces,
	}, nil
}

func (a *Inspire) AnalyzeEmotion(ctx context.Context, imgBytes []byte) (*recognition.EmotionResult, error) {
	resp, err := a.analyzePhoto(ctx, endpointPhotoEmotion, imgBytes)
	if err != nil {
		return nil, err
	}

	result := photoResult(resp)
	return &recognition.EmotionResult{
		Image:     result.Image,
		FaceCount: result.FaceCount,
		Faces:     result.Faces,
	}, nil
}

func (a *Inspire) GetBestFaceEmbedding(ctx context.Context, imgBytes []byte) ([]float64, error) {
	resp, err := a.analyzePhoto(ctx, endpointPhotoBasic, imgBytes)
	if err != nil {
		return nil, fmt.Errorf("inspire best face embedding: %w", err)
	}

	face, err := bestFace(resp)
	if err != nil {
		return nil, err
	}

	return validFaceEmbedding(*face)
}

func (a *Inspire) GetOneFaceEmbedding(ctx context.Context, imgBytes []byte) ([]float64, error) {
	resp, err := a.analyzePhoto(ctx, endpointPhotoBasic, imgBytes)
	if err != nil {
		return nil, fmt.Errorf("inspire one face embedding: %w", err)
	}

	face, err := oneFace(resp)
	if err != nil {
		return nil, err
	}

	return validFaceEmbedding(*face)
}

func (a *Inspire) AnalyzeLivenessEmbedding(ctx context.Context, frames [][]byte) (*recognition.LivenessResult, error) {
	if len(frames) == 0 {
		return nil, recognition.ErrInvalidImage
	}

	formFiles := make([]formFile, 0, len(frames))
	for i, frame := range frames {
		if len(frame) == 0 {
			return nil, recognition.ErrInvalidImage
		}

		formFiles = append(formFiles, formFile{
			field:    "frames",
			filename: fmt.Sprintf("frame_%03d.jpg", i+1),
			content:  frame,
		})
	}

	respBody, err := a.postMultipart(ctx, endpointVideoLivenessEmbedding, formFiles)
	if err != nil {
		return nil, fmt.Errorf("inspire liveness embedding: %w", err)
	}

	resp, err := BytesFromVideoLivenessEmbedding(respBody)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", recognition.ErrInvalidResponse, err)
	}
	if !resp.OK {
		return nil, recognition.ErrRecognitionFailed
	}
	if !resp.Live {
		return nil, recognition.ErrLowFaceQuality
	}
	if !resp.HasEmbedding() {
		return nil, recognition.ErrNoFaceEmbedding
	}

	return livenessResult(resp), nil
}

func (a *Inspire) GetLivenessEmbedding(ctx context.Context, frames [][]byte) ([]float64, error) {
	resp, err := a.AnalyzeLivenessEmbedding(ctx, frames)
	if err != nil {
		return nil, err
	}

	return resp.Embedding, nil
}

func (a *Inspire) analyzePhoto(ctx context.Context, endpoint string, imgBytes []byte) (*PhotoResponse, error) {
	if len(imgBytes) == 0 {
		return nil, recognition.ErrInvalidImage
	}

	respBody, err := a.postMultipart(ctx, endpoint, []formFile{{
		field:    "image",
		filename: "image.jpg",
		content:  imgBytes,
	}})
	if err != nil {
		return nil, err
	}

	resp, err := BytesFromPhoto(respBody)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", recognition.ErrInvalidResponse, err)
	}
	if !resp.OK {
		if resp.Error != nil {
			return nil, fmt.Errorf("%w: code=%s,message=%s", recognition.ErrRecognitionFailed, resp.Error.Code, resp.Error.Message)
		}
		return nil, recognition.ErrRecognitionFailed
	}

	return resp, nil
}

func bestFace(resp *PhotoResponse) (*FaceResult, error) {
	if resp == nil || resp.FaceCount == 0 || len(resp.Faces) == 0 {
		return nil, recognition.ErrNoFace
	}

	best := &resp.Faces[0]
	for i := 1; i < len(resp.Faces); i++ {
		if resp.Faces[i].Quality > best.Quality {
			best = &resp.Faces[i]
		}
	}

	return best, nil
}

func oneFace(resp *PhotoResponse) (*FaceResult, error) {
	if resp == nil || resp.FaceCount == 0 || len(resp.Faces) == 0 {
		return nil, recognition.ErrNoFace
	}
	if resp.FaceCount != 1 || len(resp.Faces) != 1 {
		return nil, recognition.ErrNotOneFace
	}

	return &resp.Faces[0], nil
}

func validFaceEmbedding(face FaceResult) ([]float64, error) {
	if len(face.Embedding) == 0 {
		return nil, recognition.ErrNoFaceEmbedding
	}
	if face.EmbeddingDim > 0 && len(face.Embedding) != face.EmbeddingDim {
		return nil, recognition.ErrInvalidEmbedding
	}

	return face.Embedding, nil
}

func photoResult(resp *PhotoResponse) *recognition.PhotoResult {
	if resp == nil {
		return nil
	}

	faces := make([]recognition.Face, 0, len(resp.Faces))
	for _, face := range resp.Faces {
		faces = append(faces, faceResult(face))
	}

	return &recognition.PhotoResult{
		Image: recognition.Image{
			Channels: resp.Image.Channels,
			Height:   resp.Image.Height,
			Width:    resp.Image.Width,
		},
		FaceCount: resp.FaceCount,
		Faces:     faces,
	}
}

func livenessResult(resp *VideoLivenessEmbeddingResponse) *recognition.LivenessResult {
	if resp == nil {
		return nil
	}

	frames := make([]recognition.Face, 0, len(resp.Frames))
	for _, frame := range resp.Frames {
		frames = append(frames, videoFrameResult(frame))
	}

	var bestFrame recognition.Face
	if resp.BestFrame != nil {
		bestFrame = videoFrameResult(*resp.BestFrame)
	}

	var embedding []float64
	if resp.Embedding != nil {
		embedding = resp.Embedding.Vector
	}

	return &recognition.LivenessResult{
		FrameCount: resp.FrameCount,
		Live:       resp.Live,
		BestFrame:  bestFrame,
		Embedding:  embedding,
		Frames:     frames,
	}
}

func faceResult(face FaceResult) recognition.Face {
	var emotion recognition.Emotion
	if face.Emotion != nil {
		emotion = recognition.Emotion{
			ID:    face.Emotion.ID,
			Label: face.Emotion.Label,
		}
	}

	return recognition.Face{
		Index:        face.Index,
		Box:          face.Box,
		DetScore:     face.DetScore,
		Embedding:    face.Embedding,
		EmbeddingDim: face.EmbeddingDim,
		Pose: recognition.Pose{
			Pitch: face.Pose.Pitch,
			Roll:  face.Pose.Roll,
			Yaw:   face.Pose.Yaw,
		},
		Quality:     face.Quality,
		RGBLiveness: face.RGBLiveness,
		Emotion:     emotion,
	}
}

func videoFrameResult(frame VideoFrameResult) recognition.Face {
	return recognition.Face{
		Index:       frame.Index,
		Box:         frame.Box,
		DetScore:    frame.DetScore,
		Pose:        recognition.Pose{Pitch: frame.Pose.Pitch, Roll: frame.Pose.Roll, Yaw: frame.Pose.Yaw},
		Quality:     frame.Quality,
		RGBLiveness: frame.RGBLiveness,
	}
}

type formFile struct {
	field    string
	filename string
	content  []byte
}

func (a *Inspire) get(ctx context.Context, endpoint string) ([]byte, error) {
	if err := a.validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = a.ctx
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpointURL(endpoint), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create request failed: %w", recognition.ErrBuildImageRequest, err)
	}

	return a.do(req)
}

func (a *Inspire) postMultipart(ctx context.Context, endpoint string, files []formFile) ([]byte, error) {
	if err := a.validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = a.ctx
	}
	if len(files) == 0 {
		return nil, recognition.ErrInvalidImage
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for _, file := range files {
		if file.field == "" || file.filename == "" || len(file.content) == 0 {
			return nil, recognition.ErrInvalidImage
		}

		part, err := writer.CreateFormFile(file.field, file.filename)
		if err != nil {
			return nil, fmt.Errorf("%w: create form file failed: %w", recognition.ErrBuildImageRequest, err)
		}
		if _, err := part.Write(file.content); err != nil {
			return nil, fmt.Errorf("%w: write image bytes failed: %w", recognition.ErrBuildImageRequest, err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("%w: close multipart writer failed: %w", recognition.ErrBuildImageRequest, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpointURL(endpoint), &body)
	if err != nil {
		return nil, fmt.Errorf("%w: create request failed: %w", recognition.ErrBuildImageRequest, err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	return a.do(req)
}

func (a *Inspire) do(req *http.Request) ([]byte, error) {
	resp, err := a.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: request failed: %w", recognition.ErrRequestFailed, err)
	}
	defer resp.Body.Close()

	reader := io.LimitReader(resp.Body, a.maxResponseSize+1)
	responseBytes, err := io.ReadAll(reader)
	if int64(len(responseBytes)) > a.maxResponseSize {
		return nil, fmt.Errorf("%w: response body too large", recognition.ErrInvalidResponse)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read response failed: %w", recognition.ErrInvalidResponse, err)
	}
	if len(responseBytes) == 0 {
		return nil, recognition.ErrInvalidResponse
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"%w: status=%d, body=%s",
			recognition.ErrInvalidResponse,
			resp.StatusCode,
			string(responseBytes),
		)
	}

	return responseBytes, nil
}

func (a *Inspire) validate() error {
	if a == nil || a.client == nil || a.ctx == nil || a.baseURL == "" || a.maxResponseSize <= 0 || a.client.Timeout <= 0 {
		return recognition.ErrInvalidState
	}

	return nil
}

func (a *Inspire) endpointURL(endpoint string) string {
	return a.baseURL + endpoint
}
