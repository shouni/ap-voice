package handlers

import (
	"net/http"

	"github.com/shouni/go-serve-kit/respond"
)

// Audio returns a signed URL for the generated audio.
//
// A person is redirected to the URL and a machine gets the URL itself. With a redirect,
// the caller would have to configure itself not to follow redirects just to receive the URL.
func (h *Handler) Audio(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.jobID(w, r)
	if !ok {
		return
	}

	wantsJSON := respond.WantsJSON(w, r)
	if wantsJSON {
		// The page knows from the listing whether audio exists before it shows a link; a machine does not.
		// Returning a signed URL when there is none would hit a 404 at the other end.
		hasAudio, err := h.repo.HasAudio(r.Context(), jobID)
		if err != nil {
			respond.ServerError(w, r, http.StatusBadGateway, err, "音声の有無を確認できませんでした", "job_id", jobID)
			return
		}
		if !hasAudio {
			respond.Error(w, r, http.StatusNotFound, "このジョブにはまだ音声がありません")
			return
		}
	}

	url, err := h.signer.SignURL(r.Context(), h.layout.AudioURI(h.bucket, jobID), http.MethodGet, signedURLExpiry)
	if err != nil {
		respond.ServerError(w, r, http.StatusBadGateway, err, "音声のURL生成に失敗しました", "job_id", jobID)
		return
	}

	if wantsJSON {
		respond.JSON(w, r, http.StatusOK, apiAudio{
			JobID:            jobID,
			AudioURI:         h.layout.AudioURI(h.bucket, jobID),
			SignedURL:        url,
			ExpiresInSeconds: int(signedURLExpiry.Seconds()),
		})
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

// apiAudio is the JSON response of GET /jobs/{jobID}/audio.
type apiAudio struct {
	JobID string `json:"job_id"`
	// AudioURI is where it is stored. It does not expire, so it serves as a record and a handle to fetch it again.
	AudioURI string `json:"audio_uri"`
	// SignedURL is a link anyone can play or download. It expires.
	SignedURL string `json:"signed_url"`
	// ExpiresInSeconds is how long SignedURL stays valid.
	ExpiresInSeconds int `json:"expires_in_seconds"`
}
