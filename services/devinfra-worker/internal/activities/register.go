package activities

import "go.temporal.io/sdk/activity"

// registrar — узкий интерфейс регистрации activity (удовлетворяется
// worker.Worker). Сужение упрощает тестирование регистрации.
type registrar interface {
	RegisterActivityWithOptions(a any, options activity.RegisterOptions)
}

// activityOptions задаёт явное имя регистрации activity (совпадает с именем,
// по которому workflow вызывает activity).
func activityOptions(name string) activity.RegisterOptions {
	return activity.RegisterOptions{Name: name}
}
