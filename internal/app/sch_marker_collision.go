package app

// markerCollisionBoxes keeps the calibrated symbol and text band as separate
// occupied rectangles. Their rectangular envelope can contain empty corners;
// markerJudgeBBox remains the conservative envelope for placement and terminals.
func markerCollisionBoxes(c layoutComp) []layoutBBox {
	if c.BBox == nil {
		return nil
	}
	boxes := []layoutBBox{*c.BBox}
	if band := flagTextBand(c); band != nil {
		boxes = append(boxes, *band)
	}
	return boxes
}

// clusterMemberCollisionBoxes leaves Members/Typed one-to-one and preserves
// each marker's existing envelope/terminal meaning. Only collision judging uses
// the separate occupied rectangles; legacy fixtures still use their member box.
func clusterMemberCollisionBoxes(c schCluster, i int, fallback layoutBBox) []layoutBBox {
	if len(c.Typed) == len(c.Members) && i < len(c.Typed) && len(c.Typed[i].CollisionBoxes) > 0 {
		return c.Typed[i].CollisionBoxes
	}
	return []layoutBBox{fallback}
}
