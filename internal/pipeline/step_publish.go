package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shouni/ap-voice/internal/domain"
)

// Signer represents only the signing that PublishStep needs.
// remoteio.Store satisfies it as is.
type Signer interface {
	SignURL(ctx context.Context, name, method string, expires time.Duration) (string, error)
}

// PublishStep is the concrete type that publishes the script.
type PublishStep struct {
	voice  domain.Voice
	signer Signer
}

// NewPublishStep creates a new PublishStep.
func NewPublishStep(voice domain.Voice, signer Signer) *PublishStep {
	return &PublishStep{
		voice:  voice,
		signer: signer,
	}
}

// Name is the step name.
func (*PublishStep) Name() string { return "publish" }

// Execute switches the artifacts stored by Command and puts the public URL on sc.PublicURL.
//
// generate goes as far as the script, everything else as far as the audio. generate makes no audio
// so that the script can be checked and corrected before moving on to synthesis.
func (r *PublishStep) Execute(ctx context.Context, sc *Context) error {
	publicURL, err := r.publishFor(ctx, sc.Request, sc.Script)
	if err != nil {
		return fmt.Errorf("failed to publish: %w", err)
	}
	sc.PublicURL = publicURL
	return nil
}

func (r *PublishStep) publishFor(ctx context.Context, req domain.Request, script domain.Script) (string, error) {
	if req.Command == domain.CommandGenerate {
		return r.PublishScript(ctx, req.OutputURI, script)
	}
	return r.Run(ctx, req.OutputURI, script)
}

// PublishScript stores only the script. generate ends here.
//
// It stops short of the audio so that the script can be checked and corrected before synthesis.
// Synthesis takes minutes, so running it first on a script that is expected to be fixed would be thrown away.
func (r *PublishStep) PublishScript(ctx context.Context, outputURI string, script domain.Script) (string, error) {
	if outputURI == "" {
		return "", errors.New("no output path (outputURI) was given")
	}

	slog.InfoContext(ctx, "saving the script", "output_path", outputURI)
	if err := r.voice.UploadScript(ctx, outputURI, script); err != nil {
		return "", fmt.Errorf("failed to save the script (%s): %w", outputURI, err)
	}
	slog.InfoContext(ctx, "script saved", "output_path", outputURI)

	// No signed URL is returned. Signing does not check that the object exists, so the URL of audio
	// not yet made could be signed, and putting it in a notification would hand out a 404 link.
	// What to look at at this stage is the script, which the detail page shows.
	return "", nil
}

// Run stores the script and then synthesises the audio.
//
// In the opposite order, a synthesis timeout would lose the script with it. On the path that makes
// script and audio together, the generated script is not stored anywhere yet. If the audio came first,
// synthesis hitting its limit would lose Gemini's output, leaving only a full redo.
// Stored first, synthesis alone can be redone from the detail page.
//
// The script is rewritten every time so that, when a corrected script is passed in, the stored script
// does not drift from what was actually spoken.
func (r *PublishStep) Run(ctx context.Context, outputURI string, script domain.Script) (string, error) {
	if outputURI == "" {
		return "", errors.New("no output path (outputURI) was given")
	}

	if err := r.voice.UploadScript(ctx, outputURI, script); err != nil {
		return "", fmt.Errorf("failed to save the script (%s): %w", outputURI, err)
	}

	slog.InfoContext(ctx, "starting speech synthesis", "output_path", outputURI)
	if err := r.voice.UploadWav(ctx, outputURI, script.Lines); err != nil {
		return "", fmt.Errorf("speech synthesis pipeline failed (%s): %w", outputURI, err)
	}
	slog.InfoContext(ctx, "speech synthesis finished", "output_path", outputURI)

	return r.publicURLOrEmpty(ctx, outputURI), nil
}

// publicURLOrEmpty returns a signed URL. Processing continues even if one cannot be made.
// The artifacts are stored, so there is no reason to treat a missing URL as a failure.
func (r *PublishStep) publicURLOrEmpty(ctx context.Context, outputURI string) string {
	publicURL, err := r.buildPublicURL(ctx, outputURI)
	if err != nil {
		slog.WarnContext(ctx, "failed to build the public URL; continuing without one", "output_path", outputURI, "error", err)
		return ""
	}
	return publicURL
}

func (r *PublishStep) buildPublicURL(ctx context.Context, outputURI string) (string, error) {
	if r.signer == nil {
		return "", nil
	}
	return r.signer.SignURL(ctx, outputURI, "GET", time.Hour)
}
