package minimax

import (
	"fmt"
	"mime/multipart"
	"slices"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
)

type miniMaxTranscriptionRequest struct {
	Model          string
	File           []byte
	Filename       string
	Language       string
	ResponseFormat string
	TimestampLevel string
	Stream         bool
}

func toMiniMaxTranscriptionRequest(bifrostReq *schemas.BifrostTranscriptionRequest, stream bool) (*miniMaxTranscriptionRequest, error) {
	if bifrostReq == nil || bifrostReq.Input == nil || len(bifrostReq.Input.File) == 0 {
		return nil, fmt.Errorf("transcription file is required")
	}
	req := &miniMaxTranscriptionRequest{
		Model:    bifrostReq.Model,
		File:     bifrostReq.Input.File,
		Filename: bifrostReq.Input.Filename,
		Stream:   stream,
	}
	params := bifrostReq.Params
	if params == nil {
		return req, nil
	}
	if params.Language != nil {
		req.Language = strings.TrimSpace(*params.Language)
	}
	if params.ResponseFormat != nil {
		switch format := *params.ResponseFormat; format {
		case "json", "text", "":
		case "verbose_json", "srt", "vtt":
			req.ResponseFormat = format
		default:
			return nil, fmt.Errorf("unsupported response_format %q for minimax transcription", format)
		}
	}
	switch {
	case slices.Contains(params.TimestampGranularities, "word"):
		req.TimestampLevel = "word"
	case slices.Contains(params.TimestampGranularities, "segment"):
		req.TimestampLevel = "sentence"
	}
	if v, ok := params.ExtraParams["timestamp_level"].(string); ok && v != "" {
		req.TimestampLevel = v
	}
	if req.TimestampLevel != "" && req.ResponseFormat == "" {

		req.ResponseFormat = "verbose_json"
	}
	if stream && req.ResponseFormat != "" {
		return nil, fmt.Errorf("minimax streaming transcription only supports the json response format and cannot return timestamps")
	}
	return req, nil
}

func writeMiniMaxTranscriptionMultipart(writer *multipart.Writer, req *miniMaxTranscriptionRequest) *schemas.BifrostError {
	filename := req.Filename
	if filename == "" {
		filename = providerUtils.AudioFilenameFromBytes(req.File)
	}
	fileWriter, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return providerUtils.NewBifrostOperationError("failed to create file field", err)
	}
	if _, err := fileWriter.Write(req.File); err != nil {
		return providerUtils.NewBifrostOperationError("failed to write file data", err)
	}
	fields := [][2]string{{"model", req.Model}, {"response_format", req.ResponseFormat}, {"timestamp_level", req.TimestampLevel}}
	if req.Stream {
		fields = append(fields, [2]string{"stream", "true"})
	}
	for _, field := range fields {
		if field[1] == "" {
			continue
		}
		if err := writer.WriteField(field[0], field[1]); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write "+field[0]+" field", err)
		}
	}
	return nil
}

func ToBifrostTranscriptionResponse(resp *MiniMaxTranscriptionResponse, timestampLevel string) *schemas.BifrostTranscriptionResponse {
	out := &schemas.BifrostTranscriptionResponse{
		Text:     resp.Text,
		Duration: resp.Duration,
		Task:     schemas.Ptr("transcribe"),
	}
	if resp.Duration != nil {
		out.Usage = &schemas.TranscriptionUsage{Type: "duration", Seconds: resp.Duration}
	}
	for _, seg := range resp.Segments {
		if timestampLevel == "word" {
			word := schemas.TranscriptionWord{Word: strings.TrimSpace(seg.Text), Start: seg.Start, End: seg.End}
			if seg.Speaker != "" {
				word.Speaker = schemas.Ptr(seg.Speaker)
			}
			out.Words = append(out.Words, word)
			continue
		}
		out.Segments = append(out.Segments, schemas.TranscriptionSegment{ID: seg.ID, Start: seg.Start, End: seg.End, Text: seg.Text})
	}
	return out
}
