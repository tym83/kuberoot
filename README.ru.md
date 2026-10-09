# kuberoot

[English version](README.md)

kuberoot собирает дистрибутивы Kubernetes, которые загружаются прямо на железо. Каждый из них — один образ со своим ядром Linux, маленьким init, который поднимает Kubernetes, и нодой, которой управляют через сам API Kubernetes: нет SSH, нет оболочки и нет отдельной утилиты для ноды, до неё дотягивается `kubectl`. Всё поверх базы — пакеты [kubepkg](https://github.com/tym83/kubepkg), так что дистрибутив — это база плюс метапакет, как дистрибутив Linux — это ядро плюс пакеты.

В репозитории собираются два дистрибутива: `edge` — кластер общего назначения на одну или несколько нод, и `router` — Kubernetes без контейнеров, работающий сетевым маршрутизатором.

> Статус: эксперимент. Дистрибутив `edge` проходит набор тестов на соответствие Kubernetes (462 из 462 на кластере из трёх нод, Kubernetes 1.37.1), но API ноды и раскладка её состояния ещё могут меняться.

## Из чего состоит нода

- **Ядро**: Linux 6.18, собранное под дистрибутив, все драйверы встроены. Нода загружает только модули, которые назвал её профиль (DRBD для реплицируемого хранилища), подписанные ключом сборки; после них загрузка модулей выключается.
- **kinit**: PID 1. Монтирует корень только для чтения и раздел состояния, поднимает сеть, выпускает сертификаты ноды и запускает службы её роли: containerd, kubelet и kube-proxy на всех нодах, а на control plane ещё kine (хранилище кластера на SQLite), API-сервер, controller manager и планировщик.
- **API ноды**: каждая нода обслуживает `node.kuberoot.dev`, подключённый к API кластера, поэтому `kubectl get disks`, `kubectl get bootentries` или `kubectl create upgrade` действуют на ноду. Он работает и до того, как появится кластер, — так ноду и устанавливают.
- **Сеть подов**: VXLAN между нодами, или обычные маршруты в плоской сети, или никакой — для CNI-пакета вроде Cilium.
- **Пакеты**: оператор kubepkg работает на control plane и ставит пакеты дистрибутива; метапакет `edge` и пакеты платформы лежат в [kubepkg-recipes](https://github.com/tym83/kubepkg-recipes).

## Дистрибутив router

`router` оставляет от Kubernetes только то, что хранит конфигурацию и следит за ней: kine, API-сервер, controller manager для сборки мусора и API ноды. Нет kubelet, среды запуска контейнеров, kube-proxy и планировщика. Конфигурация маршрутизатора — ресурсы `router.kuberoot.dev`, с RBAC, аудитом, watch и GitOps, как у любых ресурсов, а `kuberoot-router` приводит к ним ноду:

| Ресурс | Что настраивает |
|---|---|
| `Interface` | адреса и MTU линка или VLAN на родительском линке |
| `Route` | статический маршрут |
| `NATRule` | маскарадинг за исходящим линком или проброс порта на хост |
| `FirewallZone`, `FirewallRule` | зоны линков: что может достучаться до маршрутизатора и что может ходить между зонами |
| `DHCPServer` | DHCP на линке и DNS-кэш для него |
| `BGPRouter`, `BGPPeer` | AS маршрутизатора, анонсируемые сети и BGP-сессии |
| `Safeguard` | изменения на испытательном сроке, которые откатываются без подтверждения |

Домашний шлюз с LAN на `eth1` за внешним `eth0`:

```yaml
apiVersion: router.kuberoot.dev/v1alpha1
kind: Interface
metadata: {name: lan}
spec: {link: eth1, addresses: [192.168.10.1/24]}
---
apiVersion: router.kuberoot.dev/v1alpha1
kind: NATRule
metadata: {name: out}
spec: {masquerade: {outLink: eth0, sources: [192.168.10.0/24]}}
---
apiVersion: router.kuberoot.dev/v1alpha1
kind: DHCPServer
metadata: {name: lan}
spec: {link: eth1, rangeStart: 192.168.10.100, rangeEnd: 192.168.10.199}
---
apiVersion: router.kuberoot.dev/v1alpha1
kind: FirewallZone
metadata: {name: wan}
spec: {links: [eth0], input: Drop, management: true}
---
apiVersion: router.kuberoot.dev/v1alpha1
kind: FirewallZone
metadata: {name: lan}
spec: {links: [eth1], input: Accept, forwardTo: [wan]}
```

Каждый ресурс пишет в статус, исполняет ли его нода, а если нет — почему; интерфейсы показывают своё состояние, DHCP-серверы — аренды, BGP-соседи — сессии. Файрвол фильтрует только когда есть хотя бы одна зона, а API маршрутизатора остаётся открытым в зонах управления, что бы ни говорили правила. С `Safeguard` каждое изменение работает на испытательном сроке: если `spec.confirm` не назовёт его ревизию вовремя, маршрутизатор возвращается к последней подтверждённой конфигурации, так что изменение, отрезавшее администраторов, отменяет себя само.

## API ноды

| Ресурс | Что делает |
|---|---|
| `osconfigs` | настройки операционной системы ноды |
| `nodeservices` | службы, которые запускает kinit, их состояние и логи |
| `disks` | блочные устройства |
| `installations` | записывает систему на диск, с загрузочного носителя |
| `bootentries` | два корневых слота и какой из них загружается |
| `upgrades` | записывает подписанный выпуск в неактивный слот |
| `memberships`, `jointickets` | присоединяют ноды к кластеру |
| `statebackups` | архивы состояния control plane |

## Установка и обновление

Нода загружается с носителя (`mkimage` пишет образ диска с ядром, initramfs и корневой системой). В живой системе `Installation` размечает диск: EFI-раздел с systemd-boot, два корневых слота и раздел состояния. Вместе с системой на диск переезжают идентичность ноды, её членство в кластере и, на control plane, снимок хранилища кластера.

`Upgrade` скачивает подписанный выпуск, пишет его в неактивный слот и загружает на испытательный срок: если за пять минут все службы не работают без перезапусков и нода не стала Ready, она перезагружается, а когда попытки кончаются — снова загружает прежний слот.

## Резервные копии control plane

Control plane держит своё состояние в одном месте: хранилище кластера, его удостоверяющие центры и идентичность ноды. API ноды архивирует его каждый час и по запросу (`kubectl create statebackup`), хранит последние двенадцать на ноде и, если так сказано в Secret `kube-system/kuberoot-state-backup`, выгружает в S3-совместимое хранилище:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: kuberoot-state-backup
  namespace: kube-system
stringData:
  interval: 1h
  keep: "12"
  encryptionRecipient: age1...        # архивы шифруются на этот ключ age
  s3Endpoint: https://s3.example.org
  s3Bucket: backups
  s3AccessKey: ...
  s3SecretKey: ...
```

В архивах ключи кластера и все его Secret, поэтому с ноды они уходят только зашифрованными (`age-keygen` делает пару ключей; секретный остаётся у вас). Чтобы вернуть потерянный control plane на новом железе, загрузите новую машину с носителя и установите её из копии:

```yaml
apiVersion: node.kuberoot.dev/v1alpha1
kind: Installation
metadata:
  name: restore
spec:
  disk: vda
  reboot: true
  restore:
    url: https://...      # подписанная ссылка на архив
    sha256: ...           # как его показывает StateBackup
    identity: AGE-SECRET-KEY-1...
```

Нода возвращается той же нодой — с тем же именем, сертификатами и кластером.

## Устройство репозитория

| Путь | |
|---|---|
| `cmd/kinit` | PID 1 |
| `cmd/kuberoot-node` | сервер API ноды |
| `cmd/kuberoot-installer` | консольный интерфейс установщика |
| `cmd/kuberoot-intents` | переводит типизированные намерения в примитивы Kubernetes |
| `cmd/kuberoot-router` | контроллер маршрутизатора |
| `cmd/mkimage`, `cmd/kuberoot-release` | загрузочные носители и подписанные выпуски |
| `pkg/` | библиотеки, на которых они построены |
| `distros/edge`, `distros/router` | дистрибутивы: службы, модули, аддоны, пакеты |
| `kernel/` | конфигурация и версии ядра |
| `rootfs/` | файлы корневой системы |
| `scripts/` | шаги сборки |

## Сборка

Сборка идёт в контейнере с инструментами, а не на рабочей машине: в `scripts/` лежат шаги (ядро, DRBD, программы, корневая система, initramfs, выпуск). `hack/hetzner.sh` управляет подом-сборщиком в кластере Kubernetes и запускает их там; в `hack/hetzner/` — сборщик, реестр и описания ВМ, на которых автор проверяет ноды.

```sh
hack/hetzner.sh build amd64      # ядро, если его нет, программы, корневая система, initramfs
hack/hetzner.sh bundle amd64     # подписанный выпуск
hack/hetzner.sh media amd64      # загрузочный носитель
hack/hetzner.sh test             # go test ./...
```

## Лицензия

Apache License 2.0, см. [LICENSE](LICENSE).
