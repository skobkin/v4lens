package domain

import "fmt"

// ControlType is the normalized V4L2 control type. Compound/array payloads
// (elems > 1, non-scalar U8/U16/U32) stay visible as TypeUnsupported
// descriptors but are not editable in the MVP.
type ControlType int8

const (
	TypeInteger ControlType = iota
	TypeInteger64
	TypeBoolean
	TypeMenu
	TypeIntegerMenu
	TypeButton
	TypeString
	TypeBitmask
	// TypeU8 is the scalar u8 payload control type; TypeU16 and TypeU32
	// likewise (all scalar, elems == 1).
	TypeU8
	TypeU16
	TypeU32
	// TypeUnsupported covers compound/array/payload shapes without an
	// editable scalar form.
	TypeUnsupported
)

// String returns a short type name for diagnostics.
func (t ControlType) String() string {
	switch t {
	case TypeInteger:

		return "integer"
	case TypeInteger64:

		return "integer64"
	case TypeBoolean:

		return "boolean"
	case TypeMenu:

		return "menu"
	case TypeIntegerMenu:

		return "integer-menu"
	case TypeButton:

		return "button"
	case TypeString:

		return "string"
	case TypeBitmask:

		return "bitmask"
	case TypeU8:

		return "u8"
	case TypeU16:

		return "u16"
	case TypeU32:

		return "u32"
	default:

		return "unsupported"
	}
}

// ControlFlag carries the normalized V4L2 control flags V4Lens acts on.
// Unknown kernel flag bits are preserved in ControlDescriptor.RawFlags and
// never break parsing (newer kernels add bits, e.g. HAS_WHICH_MIN_MAX).
type ControlFlag uint32

const (
	FlagReadOnly       ControlFlag = 1 << iota // 0x0004 in the uapi
	FlagInactive                               // dependency not currently satisfied
	FlagGrabbed                                // owned by another fh/application
	FlagVolatile                               // value may change spontaneously
	FlagUpdate                                 // writing may affect other controls
	FlagSlider                                 // driver suggests slider rendering
	FlagWriteOnly                              // no truthful read-back exists
	FlagExecuteOnWrite                         // action semantics, not state
	FlagModifyLayout                           // may change buffer layout mid-stream
	FlagHasPayload
	FlagDynamicArray
)

// ControlDescriptor is the driver-reported description of one control.
// CTRL_CLASS pseudo-controls never appear here: the backend consumes them
// as grouping metadata and records the class in ClassID/ClassName.
type ControlDescriptor struct {
	ID   uint32
	Name string
	Type ControlType

	// ClassID / ClassName come from the CTRL_CLASS pseudo-control the
	// control belongs to (e.g. 0x00980001 "User Controls").
	ClassID   uint32
	ClassName string

	// Min/Max/Step/Default are faithful int64 V4L2 values. For menu types
	// they are index bounds.
	Min, Max, Step, Default int64

	// MenuItems lists menu entries for menu and integer-menu controls.
	MenuItems []MenuItem

	// ElemSize and Elems distinguish scalar payload controls (Elems == 1)
	// from array/compound ones; Dims preserves the driver dimensions.
	ElemSize int
	Elems    int
	Dims     [4]int

	Flags     ControlFlag
	RawFlags  uint32
	CurFlags  ControlFlag // live flags observed at the last value read
	RawCurFlg uint32
}

// Scalar reports whether the control has exactly one element. HAS_PAYLOAD
// alone does not mean unsupported: scalar U8/U16/U32/String stay editable.
func (d ControlDescriptor) Scalar() bool {
	return d.Elems <= 1
}

// SimpleType reports whether the control's type has an editable MVP form
// (possibly gated by flags such as READ_ONLY).
func (d ControlDescriptor) SimpleType() bool {
	switch d.Type {
	case TypeInteger, TypeInteger64, TypeBoolean, TypeMenu,
		TypeIntegerMenu, TypeButton, TypeString, TypeBitmask,
		TypeU8, TypeU16, TypeU32:

		return true
	default:

		return false
	}
}

// Editable reports whether V4Lens offers an editor for this descriptor:
// a simple scalar type, not read-only, not an unsupported compound shape.
// Buttons are editable (they are pressed, not set).
func (d ControlDescriptor) Editable() bool {
	if !d.Scalar() || !d.SimpleType() {
		return false
	}

	if d.Type == TypeButton {
		return !d.Flags.Has(FlagReadOnly)
	}

	return !d.Flags.Has(FlagReadOnly)
}

// Persistable reports whether Save/dirty tracking includes this control by
// default: writable state controls only. READ_ONLY (no desired state),
// BUTTON and EXECUTE_ON_WRITE (actions), WRITE_ONLY (no truthful read-back),
// VOLATILE (may change spontaneously) and unsupported compounds are excluded
// from profile-managed state.
func (d ControlDescriptor) Persistable() bool {
	if !d.Scalar() || !d.SimpleType() || d.Type == TypeButton {
		return false
	}

	if d.Flags.Has(FlagReadOnly) || d.Flags.Has(FlagWriteOnly) ||
		d.Flags.Has(FlagExecuteOnWrite) || d.Flags.Has(FlagVolatile) {
		return false
	}

	return true
}

// Has tests a normalized flag.
func (f ControlFlag) Has(flag ControlFlag) bool {
	return f&flag != 0
}

// MenuItem is one menu entry. Integer menus carry a numeric value instead
// of a name.
type MenuItem struct {
	Index int64
	Name  string
	IsInt bool
	Int   int64
}

// Label returns the display text of the entry.
func (m MenuItem) Label() string {
	if m.IsInt {
		return fmt.Sprintf("%d", m.Int)
	}

	return m.Name
}

// ControlValue is a typed control value. Menu selections (and bitmask
// values) are carried in Int; Bool only for booleans; Str only for string
// controls. The zero Type is TypeInteger, so integer values need no extra
// setup.
type ControlValue struct {
	ID   uint32
	Type ControlType
	Bool bool
	Int  int64
	Str  string
}

// NewBoolValue builds a boolean control value.
func NewBoolValue(id uint32, v bool) ControlValue {
	return ControlValue{ID: id, Type: TypeBoolean, Bool: v}
}

// NewIntValue builds an integer-family control value (integer, integer64,
// menu selection, bitmask bits, scalar U8/U16/U32).
func NewIntValue(id uint32, t ControlType, v int64) ControlValue {
	return ControlValue{ID: id, Type: t, Int: v}
}

// NewStringValue builds a string control value.
func NewStringValue(id uint32, s string) ControlValue {
	return ControlValue{ID: id, Type: TypeString, Str: s}
}

// String renders the value for logs and dialogs.
func (v ControlValue) String() string {
	switch v.Type {
	case TypeBoolean:

		return fmt.Sprintf("%t", v.Bool)
	case TypeString:

		return v.Str
	default:

		return fmt.Sprintf("%d", v.Int)
	}
}

// WriteResult distinguishes acceptance from the effective hardware value.
// Effective is nil whenever no truthful read-back exists (WRITE_ONLY
// controls); the requested value is NEVER substituted for a read-back.
type WriteResult struct {
	Accepted  bool
	Effective *ControlValue
}
