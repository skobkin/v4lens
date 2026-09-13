package v4l

import (
	"fmt"
	"unsafe"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// ctrlTypeToDomain maps a V4L2 control type to the domain type. Array
// payload shapes (elems > 1) are normalized to TypeUnsupported by the
// caller; the type mapping itself stays 1:1.
func ctrlTypeToDomain(t uint32) domain.ControlType {
	switch t {
	case ctrlTypeInteger:

		return domain.TypeInteger
	case ctrlTypeBoolean:

		return domain.TypeBoolean
	case ctrlTypeMenu:

		return domain.TypeMenu
	case ctrlTypeButton:

		return domain.TypeButton
	case ctrlTypeInteger64:

		return domain.TypeInteger64
	case ctrlTypeString:

		return domain.TypeString
	case ctrlTypeBitmask:

		return domain.TypeBitmask
	case ctrlTypeIntegerMenu:

		return domain.TypeIntegerMenu
	case ctrlTypeU8:

		return domain.TypeU8
	case ctrlTypeU16:

		return domain.TypeU16
	case ctrlTypeU32:

		return domain.TypeU32
	default:

		return domain.TypeUnsupported
	}
}

// ctrlFlagsToDomain normalizes the flag bits V4Lens acts on. Unknown bits
// survive only in RawFlags — newer kernels keep adding bits (D013).
func ctrlFlagsToDomain(f uint32) domain.ControlFlag {
	var out domain.ControlFlag

	if f&ctrlFlagReadOnly != 0 {
		out |= domain.FlagReadOnly
	}

	if f&ctrlFlagInactive != 0 {
		out |= domain.FlagInactive
	}

	if f&ctrlFlagGrabbed != 0 {
		out |= domain.FlagGrabbed
	}

	if f&ctrlFlagVolatile != 0 {
		out |= domain.FlagVolatile
	}

	if f&ctrlFlagUpdate != 0 {
		out |= domain.FlagUpdate
	}

	if f&ctrlFlagSlider != 0 {
		out |= domain.FlagSlider
	}

	if f&ctrlFlagWriteOnly != 0 {
		out |= domain.FlagWriteOnly
	}

	if f&ctrlFlagExecute != 0 {
		out |= domain.FlagExecuteOnWrite
	}

	if f&ctrlFlagModLayout != 0 {
		out |= domain.FlagModifyLayout
	}

	if f&ctrlFlagHasPayload != 0 {
		out |= domain.FlagHasPayload
	}

	if f&ctrlFlagDynArray != 0 {
		out |= domain.FlagDynamicArray
	}

	return out
}

// EnumerateControls builds the full control tree: QUERY_EXT_CTRL walk,
// QUERYMENU expansion and CTRL_CLASS consumption as grouping metadata.
// CTRL_CLASS pseudo-controls never become descriptors (D013 + plan).
func EnumerateControls(fd uintptr) ([]domain.ControlDescriptor, error) {
	raw, err := enumerateAllControls(fd)
	if err != nil {
		return nil, fmt.Errorf("enumerate controls: %w", err)
	}

	classes := map[uint32]string{}

	var out []domain.ControlDescriptor

	for _, q := range raw {
		if q.Type == ctrlTypeCtrlClass {
			classes[q.ID] = cString(q.Name[:])

			continue
		}

		t := ctrlTypeToDomain(q.Type)
		elems := int(q.Elems)

		// Array/compound payloads are visible but not editable in MVP.
		if elems > 1 || t == domain.TypeUnsupported {
			t = domain.TypeUnsupported
		}

		d := domain.ControlDescriptor{
			ID:        q.ID,
			Name:      cString(q.Name[:]),
			Type:      t,
			Min:       q.Minimum,
			Max:       q.Maximum,
			Step:      q.Step,
			Default:   q.Default,
			ElemSize:  int(q.ElemSize),
			Elems:     elems,
			Flags:     ctrlFlagsToDomain(q.Flags),
			RawFlags:  q.Flags,
			CurFlags:  ctrlFlagsToDomain(q.Flags),
			RawCurFlg: q.Flags,
		}

		for i, v := range q.Dims {
			d.Dims[i] = int(v)
		}

		out = append(out, d)
	}

	// Resolve class metadata: a control's class id is (ID & 0xFFFF0000) | 1
	// (e.g. 0x00980900 -> 0x00980001).
	for i := range out {
		if name, ok := classes[out[i].ID&0xFFFF0000|0x00000001]; ok {
			out[i].ClassID = out[i].ID&0xFFFF0000 | 0x00000001
			out[i].ClassName = name
		}
	}

	// Expand menu items.
	for i := range out {
		switch out[i].Type {
		case domain.TypeMenu, domain.TypeIntegerMenu:

			out[i].MenuItems = enumMenuItems(fd, out[i])
		default:
		}
	}

	return out, nil
}

// enumMenuItems walks QUERYMENU from Min to Max, tolerating sparse holes
// (never an error — a driver may report non-contiguous indices).
func enumMenuItems(fd uintptr, d domain.ControlDescriptor) []domain.MenuItem {
	isInt := d.Type == domain.TypeIntegerMenu
	var items []domain.MenuItem

	for idx := d.Min; idx <= d.Max && int(idx-d.Min) < 4096; idx++ {
		m, err := queryMenu(fd, d.ID, idx)
		if err != nil {
			continue // sparse menu hole
		}

		if isInt {
			items = append(items, domain.MenuItem{Index: idx, IsInt: true, Int: m.intValue()})

			continue
		}

		items = append(items, domain.MenuItem{Index: idx, Name: cString(m.Name[:])})
	}

	return items
}

// ReadControl reads one control value (G_EXT_CTRLS) and refreshes the live
// INACTIVE/GRABBED flags from QUERY_EXT_CTRL.
func ReadControl(fd uintptr, d domain.ControlDescriptor) (domain.ControlValue, domain.ControlDescriptor, error) {
	if d.Type == domain.TypeUnsupported {
		return domain.ControlValue{}, d, domain.ErrUnsupported
	}

	is64 := d.Type == domain.TypeInteger64
	var val int64
	var err error

	switch d.Type {
	case domain.TypeString:
		// String read-back requires a payload buffer sized via QUERYCTRL
		// (elem_size*elems); MVP reads strings through a bounded buffer.
		buf := make([]byte, d.ElemSize*d.Elems)
		val, err = extControlString(fd, d.ID, buf)

		if err == nil {
			v := domain.NewStringValue(d.ID, cString(buf))
			applyLiveFlags(fd, &d)

			return v, d, nil
		}
	default:
		val, err = extControlValue(fd, d.ID, is64)
	}

	if err != nil {
		return domain.ControlValue{}, d, wrapBusy(fmt.Errorf("read control 0x%08x: %w", d.ID, err))
	}

	applyLiveFlags(fd, &d)

	v := domain.ControlValue{ID: d.ID, Type: d.Type}

	switch d.Type {
	case domain.TypeBoolean:
		v.Bool = val != 0
	default:
		v.Int = val
	}

	return v, d, nil
}

// extControlString reads a string control's payload into buf.
func extControlString(fd uintptr, id uint32, buf []byte) (int64, error) {
	ctl := v4l2ExtControl{ID: id, Size: uint32(len(buf))}                  //nolint:gosec // payload size from QUERY_EXT_CTRL elem_size*elems
	*(*unsafe.Pointer)(unsafe.Pointer(&ctl.VLo)) = unsafe.Pointer(&buf[0]) //nolint:gosec // pointer to caller-owned payload
	ctrls := v4l2ExtControls{Count: 1, Controls: unsafe.Pointer(&ctl)}     //nolint:gosec // pointer to local uapi struct

	if err := ioctl(fd, ReqsOf().GExtCtrls, unsafe.Pointer(&ctrls)); err != nil { //nolint:gosec // pointer to local uapi struct
		return 0, err
	}

	return int64(ctl.Size), nil
}

// applyLiveFlags refreshes the descriptor's CurFlags from the driver.
func applyLiveFlags(fd uintptr, d *domain.ControlDescriptor) {
	if flags, err := extControlFlags(fd, d.ID); err == nil {
		d.CurFlags = ctrlFlagsToDomain(flags)
		d.RawCurFlg = flags
	}
}

// WriteControl writes one control via S_EXT_CTRLS (no pre-query; kernel
// clamps) and returns WriteResult: Accepted plus the truthful Effective
// read-back when the control is readable. WRITE_ONLY controls yield
// Accepted with Effective == nil — the requested value is never disguised
// as a read-back.
func WriteControl(fd uintptr, d domain.ControlDescriptor, v domain.ControlValue) (domain.WriteResult, error) {
	if d.Type == domain.TypeUnsupported || !d.Scalar() {
		return domain.WriteResult{}, domain.ErrUnsupported
	}

	is64 := d.Type == domain.TypeInteger64
	var err error

	switch d.Type {
	case domain.TypeBoolean:
		b := int64(0)

		if v.Bool {
			b = 1
		}

		err = setExtControlValue(fd, d.ID, b, nil)
	case domain.TypeString:
		err = setExtControlValue(fd, d.ID, 0, []byte(v.Str))
	default:
		err = setExtControlValue(fd, d.ID, v.Int, nil)
	}

	if err != nil {
		return domain.WriteResult{}, wrapBusy(fmt.Errorf("write control 0x%08x: %w", d.ID, err))
	}

	res := domain.WriteResult{Accepted: true}

	if d.Flags.Has(domain.FlagWriteOnly) {
		return res, nil // no truthful read-back exists
	}

	val, rerr := extControlValue(fd, d.ID, is64)
	if rerr != nil {
		// The write was accepted but the read-back failed; report honestly
		// as accepted-without-effective instead of failing the write.
		return res, nil //nolint:nilerr // acceptance and read-back are distinct outcomes by contract
	}

	switch d.Type {
	case domain.TypeBoolean:
		eff := domain.NewBoolValue(d.ID, val != 0)
		res.Effective = &eff
	case domain.TypeMenu, domain.TypeIntegerMenu, domain.TypeBitmask,
		domain.TypeInteger, domain.TypeInteger64, domain.TypeU8, domain.TypeU16, domain.TypeU32:
		eff := domain.NewIntValue(d.ID, d.Type, val)
		res.Effective = &eff
	default:
		// Unsupported shapes never reach here.
	}

	return res, nil
}

// wrapBusy decorates kernel errors with the domain busy sentinel.
func wrapBusy(err error) error {
	if IsBusy(err) {
		return fmt.Errorf("%w: %w", domain.ErrBusy, err)
	}

	return err
}
