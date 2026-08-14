package api

import (
	"fmt"
)

func (r *RetentionPolicyPatch) Validate(resource *MetaResource) error {
	// auto_prune only for directories
	if r.AutoPrune != nil && resource.Type != ResourceTypeDir {
		return fmt.Errorf("auto_prune only applies to directories")
	}

	// prune_children only for directories
	if r.PruneChildren != nil && resource.Type != ResourceTypeDir {
		return fmt.Errorf("prune_children only applies to directories")
	}

	// Validate expires block
	if r.Expires != nil {
		if err := r.Expires.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// Validate checks expires patch validity
func (e *ExpiresPatch) Validate() error {
	// Cannot combine after_upload and after_download
	if e.AfterUpload != nil && e.AfterDownload != nil {
		return fmt.Errorf("cannot combine after_upload and after_download")
	}

	// Validate duration formats
	if e.AfterUpload != nil {
		if err := validateDuration(*e.AfterUpload); err != nil {
			return fmt.Errorf("invalid after_upload: %w", err)
		}
	}

	if e.AfterDownload != nil {
		if err := validateDuration(*e.AfterDownload); err != nil {
			return fmt.Errorf("invalid after_download: %w", err)
		}
	}

	// Validate timestamp format
	if e.At != nil {
		if _, err := parseTimeString(*e.At); err != nil {
			return fmt.Errorf("invalid expires.at: %w", err)
		}
	}

	return nil
}

func ApplyRetentionPatch(resource *MetaResource, patch *RetentionPolicyPatch) error {
	if err := patch.Validate(resource); err != nil {
		return err
	}

	if patch.Immutable != nil {
		resource.Immutable = patch.Immutable
	}
	if patch.AutoPrune != nil {
		resource.AutoPrune = patch.AutoPrune
	}
	if patch.PruneChildren != nil {
		resource.PruneChildren = patch.PruneChildren
	}

	if patch.Expires != nil {
		resource.ExpiryAfterUpload = nil
		resource.ExpiryAfterDownload = nil
		resource.ExpiryAt = nil

		if patch.Expires.AfterUpload != nil {
			resource.ExpiryAfterUpload = patch.Expires.AfterUpload
		}
		if patch.Expires.AfterDownload != nil {
			resource.ExpiryAfterDownload = patch.Expires.AfterDownload
		}
		if patch.Expires.At != nil {
			t, err := parseTimeString(*patch.Expires.At)
			if err != nil {
				return fmt.Errorf("invalid expires.at: %w", err)
			}
			resource.ExpiryAt = &t
		}
	}

	return nil
}
