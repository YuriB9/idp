# Вендоренные JSON-схемы Istio CRD для kubeconform

Эти схемы используются для офлайн-валидации Istio-ресурсов чарта `deploy/helm/idp`
(`helm template | kubeconform`) без живого кластера и без сетевых обращений к
каталогу схем на каждом запуске — закоммиченные файлы и есть «пин» (ADR-0024,
требование воспроизводимости).

- **Источник:** [datreeio/CRDs-catalog](https://github.com/datreeio/CRDs-catalog).
- **Зафиксированный commit:** см. `.catalog-commit`.
- **Покрытые ресурсы:** `networking.istio.io/v1` (VirtualService, Gateway,
  ServiceEntry, DestinationRule) и `security.istio.io/v1` (PeerAuthentication,
  AuthorizationPolicy).

Обновление: перекачать файлы с новым commit каталога и обновить `.catalog-commit`
(делать осознанно — это изменение пина схем).
