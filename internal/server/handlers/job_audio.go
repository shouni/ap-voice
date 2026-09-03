package handlers

import (
	"net/http"

	"github.com/shouni/go-serve-kit/respond"
)

// Audio は、生成済み音声の署名付き URL を返します。
//
// 人にはその URL へ転送し、機械には URL そのものを返します。転送で返すと、
// 呼び出し側は URL を受け取るために転送を追わない設定を書く必要があります。
func (h *Handler) Audio(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.jobID(w, r)
	if !ok {
		return
	}

	wantsJSON := respond.WantsJSON(w, r)
	if wantsJSON {
		// 画面はリンクを出す前に一覧で有無を知っていますが、機械は知りません。
		// 無い場合に署名付き URL を返すと、開いた先で 404 に当たります。
		hasAudio, err := h.repo.HasAudio(r.Context(), jobID)
		if err != nil {
			respond.Error(w, r, http.StatusBadGateway, "音声の有無を確認できませんでした")
			return
		}
		if !hasAudio {
			respond.Error(w, r, http.StatusNotFound, "このジョブにはまだ音声がありません")
			return
		}
	}

	url, err := h.signer.SignURL(r.Context(), h.layout.AudioURI(h.bucket, jobID), http.MethodGet, signedURLExpiry)
	if err != nil {
		respond.Error(w, r, http.StatusBadGateway, "音声のURL生成に失敗しました")
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

// apiAudio は GET /jobs/{jobID}/audio の JSON 応答です。
type apiAudio struct {
	JobID string `json:"job_id"`
	// AudioURI は保存先です。期限が無いので、記録や再取得の手掛かりになります。
	AudioURI string `json:"audio_uri"`
	// SignedURL は誰でも再生・取得できるリンクです。期限があります。
	SignedURL string `json:"signed_url"`
	// ExpiresInSeconds は SignedURL の有効期間です。
	ExpiresInSeconds int `json:"expires_in_seconds"`
}
