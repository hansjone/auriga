// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

type AttachmentOptions struct {
	Inline bool

	Cap int
}

const InlineAttachmentCap = 256 << 10

func (o AttachmentOptions) cap() int {
	if o.Cap > 0 {
		return o.Cap
	}
	return InlineAttachmentCap
}
