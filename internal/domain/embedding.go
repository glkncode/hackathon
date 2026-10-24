package domain

import (
	"github.com/google/uuid"
	"math"
)

const EmbeddingModel = "intfloat/multilingual-e5-small"
const EmbeddingRevision = "614241f622f53c4eeff9890bdc4f31cfecc418b3"
const EmbeddingDimensions = 384
const EmbeddingFormat = "e5-exact-prefix-v1-title-content-512-overlap64-mean-l2-max"

type EmbeddingIdentity struct {
	Model      string
	Revision   string
	Format     string
	Dimensions int
}

func E5Identity() EmbeddingIdentity {
	return EmbeddingIdentity{EmbeddingModel, EmbeddingRevision, EmbeddingFormat, EmbeddingDimensions}
}
func (m EmbeddingIdentity) Valid() bool { return m == E5Identity() }

func ValidateEmbedding(v []float64) error {
	if len(v) != EmbeddingDimensions {
		return ErrInvalidInput
	}
	norm := 0.0
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return ErrInvalidInput
		}
		norm += x * x
	}
	if math.Abs(math.Sqrt(norm)-1) > 0.001 {
		return ErrInvalidInput
	}
	return nil
}

type EmbeddingWindow struct {
	InputIndex  int `json:"input_index"`
	WindowIndex int `json:"window_index"`
	StartToken  int `json:"start_token"`
	EndToken    int `json:"end_token"`
	TotalTokens int `json:"total_content_tokens"`
}
type SemanticJob struct {
	ID              uuid.UUID
	VersionID       uuid.UUID
	LeaseToken      uuid.UUID
	Attempts        int
	Status          string
	CompletedChunks int
	TotalChunks     int
	LastError       *string
}
