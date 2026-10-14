{{- define "data-product-controller.fullname" -}}
{{- printf "%s" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Keep the descriptor and independently served example's approvals identical. */}}
{{- define "data-product-controller.uiHostOrigins" -}}
{{- $origins := prepend .Values.uiContract.additionalHostOrigins (printf "https://%s" .Values.route.host) -}}
{{- $seen := dict -}}
{{- range $origin := $origins -}}
{{- if or (gt (len $origin) 253) (not (regexMatch "^https://[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[1-9][0-9]{0,4})?$" $origin)) (hasKey $seen $origin) -}}
{{- fail "UI host origins must be distinct canonical HTTPS origins" -}}
{{- end -}}
{{- $_ := set $seen $origin true -}}
{{- $authority := trimPrefix "https://" $origin -}}
{{- $parts := splitList ":" $authority -}}
{{- $host := first $parts -}}
{{- $labels := splitList "." $host -}}
{{- $last := last $labels -}}
{{- if or (regexMatch "^[0-9]+$" $last) (hasPrefix "0x" $last) -}}
{{- if not (regexMatch "^(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(\\.(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3}$" $host) -}}
{{- fail "UI host origins require canonical IPv4 addresses" -}}
{{- end -}}
{{- else -}}
{{- range $label := $labels -}}
{{- if not (regexMatch "^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$" $label) -}}
{{- fail "UI host origins require lowercase DNS names" -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if eq (len $parts) 2 -}}
{{- $port := atoi (last $parts) -}}
{{- if or (eq $port 443) (gt $port 65535) -}}
{{- fail "UI host origins require a canonical non-default port from 1 to 65535" -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- toJson $origins -}}
{{- end -}}

{{- define "data-product-controller.labels" -}}
app.kubernetes.io/name: data-product-controller
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "data-product-controller.image" -}}
{{- if .Values.image.digest -}}
{{ printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else -}}
{{ printf "%s:%s" .Values.image.repository (.Values.image.tag | default (trimPrefix "v" .Chart.AppVersion)) }}
{{- end -}}
{{- end -}}
