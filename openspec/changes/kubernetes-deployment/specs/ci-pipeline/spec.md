## ADDED Requirements

### Requirement: Блокирующая валидация Helm-чартов
CI SHALL содержать блокирующую джобу валидации чартов деплоя: `helm lint`,
`helm template` для каждого окружения (`values-local`/`values-prod`) с прогоном
рендера через `kubeconform` (включая схемы Istio CRD) и `istioctl analyze`.
Версии `helm`, `kubeconform`, `istioctl` и набора Istio CRD-схем SHALL быть
пинованы (не `latest`), как у других блокирующих гейтов.

#### Scenario: невалидный чарт роняет CI
- **WHEN** изменение вносит ошибку в шаблон чарта или Istio-ресурс
- **THEN** джоба валидации Helm падает и блокирует merge

#### Scenario: оба окружения проверяются пинованными инструментами
- **WHEN** запускается джоба валидации Helm
- **THEN** local- и prod-overlay рендерятся и проходят `kubeconform` (со схемами
  Istio CRD) и `istioctl analyze` на закреплённых версиях инструментов
