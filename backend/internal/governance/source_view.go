package governance

import "errors"

type SourceView string

const (
	SourceViewDraft     SourceView = "draft"
	SourceViewPublished SourceView = "published"
)

type SourceViewArm struct {
	present bool
	view    SourceView
}

func AbsentSourceView() SourceViewArm {
	return SourceViewArm{}
}

func PresentSourceView(view SourceView) (SourceViewArm, error) {
	arm := SourceViewArm{present: true, view: view}
	if err := arm.Validate(); err != nil {
		return SourceViewArm{}, err
	}
	return arm, nil
}

func (arm SourceViewArm) Validate() error {
	if !arm.present {
		if arm.view != "" {
			return errors.New("governance: absent source view carries a value")
		}
		return nil
	}
	switch arm.view {
	case SourceViewDraft, SourceViewPublished:
		return nil
	default:
		return errors.New("governance: unknown source view")
	}
}

func (arm SourceViewArm) IsPresent() bool {
	return arm.present
}

func (arm SourceViewArm) View() (SourceView, bool) {
	return arm.view, arm.present
}

type ChatLifecycleEpoch uint64
