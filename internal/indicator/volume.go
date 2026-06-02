package indicator

// VolumeSpike returns true if the latest volume exceeds ratio × MA20 of volume.
func VolumeSpike(volumes []int64, volumeMA []float64, ratio float64) bool {
	n := len(volumes)
	if n == 0 || volumeMA[n-1] == 0 {
		return false
	}
	return float64(volumes[n-1]) >= volumeMA[n-1]*ratio
}

// VolumeRatio returns latest_volume / MA20_volume (0 if unavailable).
func VolumeRatio(volumes []int64, volumeMA []float64) float64 {
	n := len(volumes)
	if n == 0 || volumeMA[n-1] == 0 {
		return 0
	}
	return float64(volumes[n-1]) / volumeMA[n-1]
}

// VolumesToFloat64 converts []int64 volumes to []float64 for SMA input.
func VolumesToFloat64(volumes []int64) []float64 {
	out := make([]float64, len(volumes))
	for i, v := range volumes {
		out[i] = float64(v)
	}
	return out
}
