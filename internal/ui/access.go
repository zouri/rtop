package ui

import (
	"reflect"

	"rtop/internal/model"
)

// snapshotHasPermissionDenied walks status-aware snapshot fields so newly
// added metrics automatically participate in the access notice.
func snapshotHasPermissionDenied(snapshot model.Snapshot) bool {
	return valueHasPermissionDenied(reflect.ValueOf(snapshot))
}

func valueHasPermissionDenied(value reflect.Value) bool {
	if !value.IsValid() {
		return false
	}
	if value.CanInterface() {
		switch metric := value.Interface().(type) {
		case model.Metric:
			return metric.Status == model.PermissionDenied
		case model.TextMetric:
			return metric.Status == model.PermissionDenied
		}
	}

	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		return !value.IsNil() && valueHasPermissionDenied(value.Elem())
	case reflect.Struct:
		valueType := value.Type()
		for index := 0; index < value.NumField(); index++ {
			if !valueType.Field(index).IsExported() {
				continue
			}
			if valueHasPermissionDenied(value.Field(index)) {
				return true
			}
		}
	case reflect.Array, reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			if valueHasPermissionDenied(value.Index(index)) {
				return true
			}
		}
	}
	return false
}
