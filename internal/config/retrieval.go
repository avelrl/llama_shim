package config

import "llama_shim/internal/retrieval"

func (c Config) RetrievalConfig() retrieval.Config {
	return retrieval.Config{
		IndexBackend: c.RetrievalIndexBackend,
		Embedder: retrieval.EmbedderConfig{
			Backend: c.RetrievalEmbedderBackend,
			BaseURL: c.RetrievalEmbedderBaseURL,
			Model:   c.RetrievalEmbedderModel,
		},
		PGVector: retrieval.PGVectorConfig{
			ANN: retrieval.PGVectorANNConfig{
				Enabled:            c.RetrievalPGVectorANNEnabled,
				Method:             c.RetrievalPGVectorANNMethod,
				Metric:             c.RetrievalPGVectorANNMetric,
				Dimensions:         c.RetrievalPGVectorANNDimensions,
				HNSWM:              c.RetrievalPGVectorANNHNSWM,
				HNSWEFConstruction: c.RetrievalPGVectorANNHNSWEFConstruction,
				IVFFlatLists:       c.RetrievalPGVectorANNIVFFlatLists,
			},
		},
	}
}

func (c ShimctlConfig) RetrievalConfig() retrieval.Config {
	return retrieval.Config{
		IndexBackend: c.RetrievalIndexBackend,
		Embedder: retrieval.EmbedderConfig{
			Backend: c.RetrievalEmbedderBackend,
			BaseURL: c.RetrievalEmbedderBaseURL,
			Model:   c.RetrievalEmbedderModel,
		},
		PGVector: retrieval.PGVectorConfig{
			ANN: retrieval.PGVectorANNConfig{
				Enabled:            c.RetrievalPGVectorANNEnabled,
				Method:             c.RetrievalPGVectorANNMethod,
				Metric:             c.RetrievalPGVectorANNMetric,
				Dimensions:         c.RetrievalPGVectorANNDimensions,
				HNSWM:              c.RetrievalPGVectorANNHNSWM,
				HNSWEFConstruction: c.RetrievalPGVectorANNHNSWEFConstruction,
				IVFFlatLists:       c.RetrievalPGVectorANNIVFFlatLists,
			},
		},
	}
}
