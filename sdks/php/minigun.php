<?php

declare(strict_types=1);

/**
 * PHP SDK for MiniGun (https://github.com/ranaroussi/minigun).
 *
 * Construct once with the API base URL and bearer token, then call the
 * verb-shaped methods. All methods throw MinigunTransportException on
 * network failure or MinigunApiException on a 4xx/5xx response — catch
 * MinigunException to handle either.
 *
 *   $mg = new Minigun(getenv('MINIGUN_API_URL'), getenv('MINIGUN_API_TOKEN'));
 *   $mg->addContact('newsletter', 'alice@example.com', ['first_name' => 'Alice']);
 *   $res = $mg->sendBulk(list: 'newsletter', subject: 'Hi', from: 'Ran <r@x.com>', md: '...');
 */

class MinigunException extends \RuntimeException {}

class MinigunTransportException extends MinigunException {}

class MinigunApiException extends MinigunException
{
    public function __construct(
        public readonly int $status,
        public readonly mixed $body,
        string $message
    ) {
        parent::__construct($message);
    }
}

class Minigun
{
    public const UNSUB_LOCAL    = 'local';
    public const UNSUB_REDIRECT = 'redirect';
    public const UNSUB_EXTERNAL = 'external';

    private string $baseUrl;
    private string $token;
    private int    $connectTimeout;
    private int    $timeout;
    private string $userAgent;

    public function __construct(
        string $baseUrl,
        string $token = '',
        int    $connectTimeout = 10,
        int    $timeout = 120,
        string $userAgent = 'minigun-php/0.1'
    ) {
        if ($baseUrl === '') {
            throw new \InvalidArgumentException('baseUrl is required');
        }
        $this->baseUrl        = rtrim($baseUrl, '/');
        $this->token          = $token;
        $this->connectTimeout = $connectTimeout;
        $this->timeout        = $timeout;
        $this->userAgent      = $userAgent;
    }

    // ---------------------------------------------------------------
    // Contacts
    // ---------------------------------------------------------------

    /**
     * Upsert a contact and (re-)subscribe them to a list.
     *
     * Safe to call repeatedly with the same email: existing contacts
     * get their `params` merged and any prior unsubscribe is cleared.
     *
     * @return array{contact: array, subscription: array}
     */
    public function addContact(string $list, string $email, ?array $params = null): array
    {
        $path = '/lists/' . rawurlencode($list) . '/contacts';
        return $this->post($path, [
            'email'  => $email,
            // Force object encoding so empty {} doesn't become [] on the wire.
            'params' => $params === null ? null : (object) $params,
        ]);
    }

    /**
     * Admin-side unsubscribe by email (no token required).
     */
    public function unsubscribeContact(string $list, string $email): array
    {
        $path = '/lists/' . rawurlencode($list) . '/unsubscribe';
        return $this->post($path, ['email' => $email]);
    }

    /**
     * Permanently delete a contact and every row that references them
     * (subscriptions across all lists + unsubscribe-event audit log).
     *
     * Use this for hard-bounce cleanup so the address cannot be picked
     * up by a future bulk send. For ordinary opt-outs, prefer
     * unsubscribeContact() so the suppression record survives.
     *
     * Accepts either a contact id (c_XXXXXXXXXX) or a lowercase email.
     *
     * @return array{deleted: bool, contact: array, subscriptions_removed: int, unsub_events_removed: int}
     */
    public function deleteContact(string $idOrEmail): array
    {
        return $this->delete('/contacts/' . rawurlencode($idOrEmail));
    }

    /**
     * Paginated contacts for a list.
     */
    public function listContacts(string $list, ?string $cursor = null, int $limit = 50): array
    {
        $qs = http_build_query(array_filter([
            'cursor' => $cursor,
            'limit'  => $limit,
        ], static fn($v) => $v !== null && $v !== ''));
        $path = '/lists/' . rawurlencode($list) . '/contacts' . ($qs !== '' ? '?' . $qs : '');
        return $this->get($path);
    }

    // ---------------------------------------------------------------
    // Sends
    // ---------------------------------------------------------------

    /**
     * Send a single transactional email.
     *
     * Required: $to, $company, and one of $md/$mdFile or $html/$htmlFile.
     * $from and $subject are required too, but may be supplied via the
     * Markdown frontmatter instead of the arguments (an explicit argument
     * wins). $company is the company id or slug — MiniGun resolves the
     * sending domain from it. Pass $domain to override for this one send.
     *
     * Use named arguments (PHP 8+) so you can skip the optional $from/
     * $subject and still pass later params, e.g.
     *   $mg->sendSingle(to: $to, company: $co, mdFile: $path);
     *
     * Each body part has a "string or file path" pair. Pass at most one
     * of each pair; passing both throws. Files are read via
     * file_get_contents() at call time.
     *
     * Returns immediately (202). The worker performs the Mailgun POST
     * in the background; poll getSend() if you need the terminal status.
     *
     * @return array{send_id: string, status: string}
     */
    public function sendSingle(
        string  $to,
        string  $company,
        string  $from         = '',
        string  $subject      = '',
        ?string $md           = null,
        ?string $mdFile       = null,
        ?string $html         = null,
        ?string $htmlFile     = null,
        ?string $text         = null,
        ?string $textFile     = null,
        ?string $template     = null,
        ?string $templateFile = null,
        ?string $preheader    = null,
        ?string $replyTo      = null,
        ?string $domain       = null,
        ?string $list         = null,
        bool    $testMode     = false,
        ?string $sendAt       = null
    ): array {
        $md       = $this->resolveBody('md',       $md,       $mdFile);
        $html     = $this->resolveBody('html',     $html,     $htmlFile);
        $text     = $this->resolveBody('text',     $text,     $textFile);
        $template = $this->resolveBody('template', $template, $templateFile);

        if ($md === null && $html === null) {
            throw new \InvalidArgumentException('either $md/$mdFile or $html/$htmlFile is required');
        }

        // Markdown frontmatter fills $subject/$preheader/$from/$replyTo when
        // the caller left them empty; the block is stripped from the body.
        [$md, $fm] = $this->parseFrontmatter($md);
        $subject = $this->firstNonEmpty($subject, $fm['subject'] ?? '');
        $from    = $this->firstNonEmpty($from,    $fm['from']    ?? '');
        if ($subject === '' || $from === '') {
            throw new \InvalidArgumentException(
                '$subject and $from are required (pass them or set them in the markdown frontmatter)'
            );
        }

        return $this->post('/send/single', [
            'to'        => $to,
            'from'      => $from,
            'subject'   => $subject,
            'preheader' => $this->firstNonEmpty($preheader, $fm['preheader'] ?? ''),
            'company'   => $company,
            'list'      => $list      ?? '',
            'reply_to'  => $this->firstNonEmpty($replyTo, $fm['reply_to'] ?? ''),
            'domain'    => $domain    ?? '',
            'md'        => $md        ?? '',
            'html'      => $html      ?? '',
            'text'      => $text      ?? '',
            'template'  => $template  ?? '',
            'test_mode' => $testMode,
            'send_at'   => $sendAt    ?? '',
        ]);
    }

    /**
     * Trigger a bulk send to a list.
     *
     * Required: $list (slug or id) and one of $md / $html. $subject and
     * $from are required too, but may be supplied via the Markdown
     * frontmatter instead of the arguments (an explicit argument wins).
     * Returns 202 with a send_id while the worker drives batches in the
     * background. The first batch runs inline before the 202, so the
     * response time scales with batch_size + Mailgun's latency.
     *
     * @return array{send_id: string, status: string, total_recipients: int}
     */
    public function sendBulk(
        string  $list,
        string  $subject      = '',
        string  $from         = '',
        ?string $md           = null,
        ?string $mdFile       = null,
        ?string $html         = null,
        ?string $htmlFile     = null,
        ?string $text         = null,
        ?string $textFile     = null,
        ?string $template     = null,
        ?string $templateFile = null,
        ?string $replyTo      = null,
        ?string $preheader    = null,
        ?string $domain       = null,
        int     $batchSize    = 500,
        int     $throttleMs   = 1000,
        ?string $notifyTo     = null,
        string  $unsubMode    = self::UNSUB_LOCAL,
        ?string $unsubRedir   = null,
        ?string $unsubUrl     = null,
        bool    $testMode     = false,
        ?string $sendAt       = null
    ): array {
        $md       = $this->resolveBody('md',       $md,       $mdFile);
        $html     = $this->resolveBody('html',     $html,     $htmlFile);
        $text     = $this->resolveBody('text',     $text,     $textFile);
        $template = $this->resolveBody('template', $template, $templateFile);

        if ($md === null && $html === null) {
            throw new \InvalidArgumentException('either $md/$mdFile or $html/$htmlFile is required');
        }
        if (!in_array($unsubMode, [self::UNSUB_LOCAL, self::UNSUB_REDIRECT, self::UNSUB_EXTERNAL], true)) {
            throw new \InvalidArgumentException("unsubMode must be 'local', 'redirect', or 'external'");
        }
        if ($unsubMode === self::UNSUB_REDIRECT && ($unsubRedir === null || $unsubRedir === '')) {
            throw new \InvalidArgumentException("unsubRedir is required when unsubMode='redirect'");
        }
        if ($unsubMode === self::UNSUB_EXTERNAL && ($unsubUrl === null || $unsubUrl === '')) {
            throw new \InvalidArgumentException("unsubUrl is required when unsubMode='external'");
        }

        // Markdown frontmatter fills $subject/$preheader/$from/$replyTo when
        // the caller left them empty; the block is stripped from the body.
        [$md, $fm] = $this->parseFrontmatter($md);
        $subject = $this->firstNonEmpty($subject, $fm['subject'] ?? '');
        $from    = $this->firstNonEmpty($from,    $fm['from']    ?? '');
        if ($subject === '' || $from === '') {
            throw new \InvalidArgumentException(
                '$subject and $from are required (pass them or set them in the markdown frontmatter)'
            );
        }

        return $this->post('/send/bulk', [
            'list'         => $list,
            'subject'      => $subject,
            'from'         => $from,
            'reply_to'     => $this->firstNonEmpty($replyTo, $fm['reply_to'] ?? ''),
            'preheader'    => $this->firstNonEmpty($preheader, $fm['preheader'] ?? ''),
            'domain'       => $domain     ?? '',
            'md'           => $md         ?? '',
            'html'         => $html       ?? '',
            'text'         => $text       ?? '',
            'template'     => $template   ?? '',
            'batch_size'   => $batchSize,
            'throttle_ms'  => $throttleMs,
            'notify_email' => $notifyTo   ?? '',
            'unsub_mode'   => $unsubMode,
            'unsub_redir'  => $unsubRedir ?? '',
            'unsub_url'    => $unsubUrl   ?? '',
            'test_mode'    => $testMode,
            'send_at'      => $sendAt     ?? '',
        ]);
    }

    /**
     * Resolve a body-or-file pair. Throws if both are supplied or if the
     * file path is unreadable. Returns null when neither is supplied so
     * the caller can decide whether the field is required.
     */
    private function resolveBody(string $name, ?string $direct, ?string $file): ?string
    {
        if ($direct !== null && $file !== null) {
            throw new \InvalidArgumentException(
                "pass only one of \${$name} or \${$name}File, not both"
            );
        }
        if ($file === null) {
            return $direct;
        }
        if (!is_file($file) || !is_readable($file)) {
            throw new \InvalidArgumentException(
                "{$name}File '{$file}' does not exist or is not readable"
            );
        }
        $contents = @file_get_contents($file);
        if ($contents === false) {
            throw new \RuntimeException("failed to read {$name}File '{$file}'");
        }
        return $contents;
    }

    /**
     * Extract a leading "---" fenced frontmatter block from a Markdown body.
     * Recognized only when the first non-empty line is a fence (three or more
     * dashes) closed by a later fence line; otherwise the body is returned
     * unchanged. Only subject/preheader/from/reply_to are read; other keys
     * are ignored. The block is always stripped so it never renders.
     *
     * @return array{0: string, 1: array<string,string>} [body, meta]
     */
    private function parseFrontmatter(?string $md): array
    {
        if ($md === null || $md === '') {
            return [$md ?? '', []];
        }
        $src   = (substr($md, 0, 3) === "\xEF\xBB\xBF") ? substr($md, 3) : $md;
        $lines = explode("\n", $src);
        $n     = count($lines);

        $open = 0;
        while ($open < $n && trim($lines[$open]) === '') {
            $open++;
        }
        if ($open >= $n || !$this->isFence($lines[$open])) {
            return [$md, []];
        }

        $closing = -1;
        for ($j = $open + 1; $j < $n; $j++) {
            if ($this->isFence($lines[$j])) {
                $closing = $j;
                break;
            }
        }
        if ($closing < 0) {
            return [$md, []];
        }

        $meta = [];
        for ($i = $open + 1; $i < $closing; $i++) {
            $ln = rtrim($lines[$i], "\r");
            $c  = strpos($ln, ':');
            if ($c === false) {
                continue;
            }
            $key = strtolower(trim(substr($ln, 0, $c)));
            $val = $this->unquote(trim(substr($ln, $c + 1)));
            switch ($key) {
                case 'subject':   $meta['subject']   = $val; break;
                case 'preheader': $meta['preheader'] = $val; break;
                case 'from':      $meta['from']      = $val; break;
                case 'reply_to':
                case 'reply-to':  $meta['reply_to']  = $val; break;
            }
        }

        $body = array_slice($lines, $closing + 1);
        while (count($body) > 0 && trim($body[0]) === '') {
            array_shift($body);
        }
        return [implode("\n", $body), $meta];
    }

    /** A frontmatter delimiter: three or more dashes and nothing else. */
    private function isFence(string $line): bool
    {
        $s = trim($line);
        return strlen($s) >= 3 && trim($s, '-') === '';
    }

    private function unquote(string $s): string
    {
        $len = strlen($s);
        if ($len >= 2) {
            $a = $s[0];
            $b = $s[$len - 1];
            if (($a === '"' && $b === '"') || ($a === "'" && $b === "'")) {
                return substr($s, 1, $len - 2);
            }
        }
        return $s;
    }

    private function firstNonEmpty(?string $explicit, string $fallback): string
    {
        return ($explicit !== null && trim($explicit) !== '') ? $explicit : $fallback;
    }

    /**
     * One-shot send status + progress snapshot.
     */
    public function getSend(string $sendId): array
    {
        return $this->get('/send/' . rawurlencode($sendId));
    }

    /**
     * Aggregate stats (DB-backed; falls back to live Mailgun for fresh sends).
     */
    public function getSendStats(string $sendId): array
    {
        return $this->get('/send/' . rawurlencode($sendId) . '/stats');
    }

    /**
     * Resume a paused / failed send. Pass $force=true only if any batch
     * was left in_flight (Mailgun may already have accepted it, so a
     * retry can duplicate-send).
     */
    public function resumeSend(string $sendId, bool $force = false): array
    {
        $path = '/send/' . rawurlencode($sendId) . '/resume' . ($force ? '?force=1' : '');
        return $this->post($path, []);
    }

    /**
     * Cancel a send that has not started yet (status 'scheduled' or
     * 'queued'), transitioning it to 'cancelled'. This is the unschedule
     * path for sends created with $sendAt. Throws (409) if the send is
     * already running or in a terminal state.
     */
    public function cancelSend(string $sendId): array
    {
        return $this->post('/send/' . rawurlencode($sendId) . '/cancel', []);
    }

    /**
     * One page of per-recipient message engagement for a send (one row
     * per contact: sent/delivered timestamps, first/last open + click
     * with counts, failure/complaint/unsubscribe state). Keyset-paginated
     * by contact_id. Requires ENGAGEMENT_STATS_ENABLED on the server.
     *
     * @param array{limit?:int, cursor?:string} $opts
     */
    public function listSendRecipients(string $sendId, array $opts = []): array
    {
        $params = [];
        if (!empty($opts['limit']))  $params['limit']  = (string) $opts['limit'];
        if (!empty($opts['cursor'])) $params['cursor'] = $opts['cursor'];
        $path = '/send/' . rawurlencode($sendId) . '/recipients';
        if ($params) {
            $path .= '?' . http_build_query($params);
        }
        return $this->get($path);
    }

    /**
     * One page of the per-URL click rollup for a send (one row per
     * contact + clicked link: canonical url, first/last click, click
     * count). Keyset-paginated over (contact_id, url). Requires
     * ENGAGEMENT_STATS_ENABLED on the server. Use to segment an audience
     * by what they clicked.
     *
     * @param array{limit?:int, cursor?:string} $opts
     */
    public function listSendClicks(string $sendId, array $opts = []): array
    {
        $params = [];
        if (!empty($opts['limit']))  $params['limit']  = (string) $opts['limit'];
        if (!empty($opts['cursor'])) $params['cursor'] = $opts['cursor'];
        $path = '/send/' . rawurlencode($sendId) . '/clicks';
        if ($params) {
            $path .= '?' . http_build_query($params);
        }
        return $this->get($path);
    }

    /**
     * Per-list engagement counters for one contact. $idOrEmail accepts
     * a contact id (c_*) or email. $listId narrows to one list (id or slug).
     */
    public function getContactEngagement(string $idOrEmail, ?string $listId = null): array
    {
        $path = '/contacts/' . rawurlencode($idOrEmail) . '/engagement';
        if ($listId) {
            $path .= '?list_id=' . rawurlencode($listId);
        }
        return $this->get($path);
    }

    /**
     * Unsubscribe dormant contacts from a list. dry_run defaults to TRUE
     * server-side — explicitly pass 'dry_run' => false to commit. At
     * least one criterion must be > 0; multiple are OR'd.
     *
     * Returns ['list_id', 'dry_run', 'candidates', 'unsubscribed',
     * 'sample', 'reason_counts'].
     *
     * @param array{
     *   min_messages_since_engagement?: int,
     *   dormant_for_days?: int,
     *   no_delivery_for_days?: int,
     *   dry_run?: bool,
     *   limit?: int,
     *   sample_size?: int
     * } $opts
     */
    public function pruneList(string $listId, array $opts = []): array
    {
        $minMsg = (int) ($opts['min_messages_since_engagement'] ?? 0);
        $byRec  = (int) ($opts['dormant_for_days'] ?? 0);
        $noDel  = (int) ($opts['no_delivery_for_days'] ?? 0);
        if ($minMsg <= 0 && $byRec <= 0 && $noDel <= 0) {
            throw new \InvalidArgumentException(
                'at least one of min_messages_since_engagement, dormant_for_days, no_delivery_for_days must be > 0'
            );
        }
        $body = [
            'min_messages_since_engagement' => $minMsg,
            'dormant_for_days'              => $byRec,
            'no_delivery_for_days'          => $noDel,
        ];
        if (array_key_exists('dry_run', $opts))   $body['dry_run']    = (bool) $opts['dry_run'];
        if (!empty($opts['limit']))               $body['limit']       = (int) $opts['limit'];
        if (!empty($opts['sample_size']))         $body['sample_size'] = (int) $opts['sample_size'];
        return $this->post('/lists/' . rawurlencode($listId) . '/prune', $body);
    }

    // ---------------------------------------------------------------
    // Transport
    // ---------------------------------------------------------------

    private function get(string $path): array
    {
        return $this->request('GET', $path, null);
    }

    private function post(string $path, array $body): array
    {
        return $this->request('POST', $path, $body);
    }

    private function delete(string $path): array
    {
        return $this->request('DELETE', $path, null);
    }

    private function request(string $method, string $path, ?array $body): array
    {
        $url = $this->baseUrl . $path;

        $headers = [
            'Accept: application/json',
        ];
        if ($body !== null) {
            $headers[] = 'Content-Type: application/json';
        }
        if ($this->token !== '') {
            $headers[] = 'Authorization: Bearer ' . $this->token;
        }

        $ch = curl_init($url);
        $opts = [
            CURLOPT_CUSTOMREQUEST  => $method,
            CURLOPT_HTTPHEADER     => $headers,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_CONNECTTIMEOUT => $this->connectTimeout,
            CURLOPT_TIMEOUT        => $this->timeout,
            CURLOPT_USERAGENT      => $this->userAgent,
            CURLOPT_SSL_VERIFYPEER => true,
            CURLOPT_SSL_VERIFYHOST => 2,
        ];
        if ($body !== null) {
            $opts[CURLOPT_POSTFIELDS] = json_encode($body, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
        }
        curl_setopt_array($ch, $opts);

        try {
            $resBody = curl_exec($ch);
            $errno   = curl_errno($ch);
            $status  = curl_getinfo($ch, CURLINFO_HTTP_CODE);
        } finally {
            // curl_close($ch);
        }

        if ($errno !== 0) {
            throw new MinigunTransportException('curl error: ' . curl_strerror($errno));
        }

        $decoded = json_decode((string) $resBody, true);

        if ($status < 200 || $status >= 300) {
            $msg = is_array($decoded) && isset($decoded['error'])
                ? $decoded['error']
                : (string) $resBody;
            throw new MinigunApiException($status, $decoded ?? $resBody, "MiniGun API {$status}: {$msg}");
        }

        return is_array($decoded) ? $decoded : [];
    }
}

?>
