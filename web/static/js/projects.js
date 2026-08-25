/**
 * 项目管理与事实黑板
 */
let projectsCache = [];
let projectsCacheAll = [];
const PROJECTS_LIST_PAGE_SIZE_KEY = 'cyberstrike.projects_list_page_size';
let currentProjectId = null;
let currentProjectUpdatedAt = null;
let currentProjectTab = 'conversations';
const projectNameById = {};
let _projectsListReady = false;
let _projectsFetchPromise = null;

const PROJECT_ACTIVE_KEY = 'cyberstrike.activeProjectId';
const PROJECT_DESCRIPTION_MAX_LENGTH = 4000;
const PROJECT_NAME_MAX_LENGTH = 200;

function tp(key, opts) {
    if (typeof window.t === 'function') return window.t(key, opts);
    return key;
}

function tpFmt(key, fallback, opts) {
    const text = tp(key, opts);
    if (!text || text === key) return fallback;
    return text;
}

function requireProjectWrite() {
    if (typeof requirePermission !== 'function') return true;
    return requirePermission(
        'project:write',
        tpFmt('projects.writePermissionDenied', '当前账号仅有只读权限，无法创建或修改项目'),
    );
}

function requireProjectDelete() {
    if (typeof requirePermission !== 'function') return true;
    return requirePermission(
        'project:delete',
        tpFmt('projects.writePermissionDenied', '当前账号仅有只读权限，无法删除项目'),
    );
}

async function notifyProjectApiFailure(response, fallbackKey, fallbackText) {
    if (typeof ensureApiOk === 'function') {
        return ensureApiOk(response, tpFmt(fallbackKey, fallbackText));
    }
    if (!response || response.ok) return true;
    alert(tpFmt(fallbackKey, fallbackText));
    return false;
}

const PROJECTS_FILTER_SELECT_HANDLERS = {
    'project-vulns-filter-severity': function () { loadProjectVulnerabilities(); },
    'project-vulns-filter-status': function () { loadProjectVulnerabilities(); },
    'projects-page-size-pagination': function () { changeProjectsPageSize(); }
};
const projectsFilterSelectMap = {};
let projectsFilterSelectDocBound = false;
const PROJECTS_FILTER_SELECT_CARET = '<svg class="projects-filter-select-caret" width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M6 9l6 6 6-6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>';

function closeAllProjectsFilterSelects() {
    Object.keys(projectsFilterSelectMap).forEach(function (id) {
        const reg = projectsFilterSelectMap[id];
        if (!reg || !reg.wrapper) return;
        reg.wrapper.classList.remove('open');
        if (reg.trigger) reg.trigger.setAttribute('aria-expanded', 'false');
    });
}

function pruneProjectsFilterSelectMap(root) {
    Object.keys(projectsFilterSelectMap).forEach(function (id) {
        const select = document.getElementById(id);
        if (!select || (root && !root.contains(select))) {
            delete projectsFilterSelectMap[id];
        }
    });
}

function syncProjectsFilterSelect(select) {
    const reg = projectsFilterSelectMap[select.id];
    if (!reg) return;
    const dropdown = reg.dropdown;
    const trigger = reg.trigger;
    const valueSpan = trigger.querySelector('.projects-filter-select-value');

    dropdown.innerHTML = '';
    Array.prototype.forEach.call(select.options, function (opt) {
        const item = document.createElement('button');
        item.type = 'button';
        item.className = 'projects-filter-select-option';
        item.setAttribute('role', 'option');
        item.setAttribute('data-value', opt.value);
        if (opt.value === select.value) {
            item.classList.add('is-selected');
            item.setAttribute('aria-selected', 'true');
        } else {
            item.setAttribute('aria-selected', 'false');
        }
        const check = document.createElement('span');
        check.className = 'projects-filter-select-check';
        check.setAttribute('aria-hidden', 'true');
        check.textContent = '✓';
        const label = document.createElement('span');
        label.className = 'projects-filter-select-label';
        label.textContent = opt.textContent;
        item.appendChild(check);
        item.appendChild(label);
        dropdown.appendChild(item);
    });

    const selectedOpt = select.options[select.selectedIndex];
    if (valueSpan) {
        valueSpan.textContent = selectedOpt ? selectedOpt.textContent : '';
    }
    trigger.disabled = !!select.disabled;
    reg.wrapper.classList.toggle('is-disabled', !!select.disabled);
}

function syncAllProjectsFilterSelects() {
    Object.keys(projectsFilterSelectMap).forEach(function (id) {
        const select = document.getElementById(id);
        if (select) syncProjectsFilterSelect(select);
    });
}

function enhanceProjectsFilterSelect(select) {
    if (!select || !select.id) return;
    const existing = projectsFilterSelectMap[select.id];
    if (existing && existing.select !== select) {
        delete projectsFilterSelectMap[select.id];
    }
    if (select.dataset.projectsCustomSelect === '1') {
        syncProjectsFilterSelect(select);
        return;
    }
    select.dataset.projectsCustomSelect = '1';
    select.classList.add('projects-filter-native-select');
    select.tabIndex = -1;
    select.setAttribute('aria-hidden', 'true');

    const wrapper = document.createElement('div');
    wrapper.className = 'projects-filter-select-ui';
    if (select.id === 'projects-page-size-pagination') {
        wrapper.classList.add('projects-filter-select-ui--compact');
    }

    const trigger = document.createElement('button');
    trigger.type = 'button';
    trigger.className = 'projects-filter-select-trigger';
    trigger.setAttribute('aria-haspopup', 'listbox');
    trigger.setAttribute('aria-expanded', 'false');
    const valueSpan = document.createElement('span');
    valueSpan.className = 'projects-filter-select-value';
    trigger.appendChild(valueSpan);
    trigger.insertAdjacentHTML('beforeend', PROJECTS_FILTER_SELECT_CARET);

    const dropdown = document.createElement('div');
    dropdown.className = 'projects-filter-select-dropdown';
    dropdown.setAttribute('role', 'listbox');

    const parent = select.parentNode;
    parent.insertBefore(wrapper, select);
    wrapper.appendChild(trigger);
    wrapper.appendChild(dropdown);
    wrapper.appendChild(select);

    projectsFilterSelectMap[select.id] = { wrapper: wrapper, trigger: trigger, dropdown: dropdown, select: select };

    trigger.addEventListener('click', function (e) {
        e.stopPropagation();
        if (select.disabled) return;
        const open = wrapper.classList.contains('open');
        closeAllProjectsFilterSelects();
        if (!open) {
            wrapper.classList.add('open');
            trigger.setAttribute('aria-expanded', 'true');
        }
    });

    dropdown.addEventListener('click', function (e) {
        const opt = e.target.closest('.projects-filter-select-option');
        if (!opt) return;
        e.stopPropagation();
        const val = opt.getAttribute('data-value');
        if (val === null) return;
        if (select.value !== val) {
            select.value = val;
            select.dispatchEvent(new Event('change', { bubbles: true }));
        }
        wrapper.classList.remove('open');
        trigger.setAttribute('aria-expanded', 'false');
        syncProjectsFilterSelect(select);
    });

    select.addEventListener('change', function () {
        syncProjectsFilterSelect(select);
    });

    if (!select.dataset.projectsFilterBound) {
        select.dataset.projectsFilterBound = '1';
        const handler = PROJECTS_FILTER_SELECT_HANDLERS[select.id];
        if (typeof handler === 'function') {
            select.addEventListener('change', handler);
        }
    }

    syncProjectsFilterSelect(select);
}

function refreshProjectsFilterSelects() {
    const page = document.getElementById('page-projects');
    if (!page) return;
    pruneProjectsFilterSelectMap(page);
    page.querySelectorAll('select.projects-filter-select-native, #projects-page-size-pagination').forEach(function (select) {
        enhanceProjectsFilterSelect(select);
    });
    if (!projectsFilterSelectDocBound) {
        projectsFilterSelectDocBound = true;
        document.addEventListener('click', closeAllProjectsFilterSelects);
        document.addEventListener('keydown', function (e) {
            if (e.key === 'Escape') closeAllProjectsFilterSelects();
        });
    }
}

const FACT_ATTACK_CHAIN_PREFIXES = ['finding/', 'chain/', 'exploit/', 'poc/'];
const FACT_ATTACK_CHAIN_CATEGORIES = new Set(['finding', 'chain', 'exploit', 'poc', 'vuln']);






function getActiveProjectId() {
    try {
        return localStorage.getItem(PROJECT_ACTIVE_KEY) || '';
    } catch (e) {
        return '';
    }
}

function setActiveProjectId(id) {
    try {
        if (id) localStorage.setItem(PROJECT_ACTIVE_KEY, id);
        else localStorage.removeItem(PROJECT_ACTIVE_KEY);
    } catch (e) { /* ignore */ }
}

function rebuildProjectNameMap(list) {
    Object.keys(projectNameById).forEach((k) => delete projectNameById[k]);
    (list || []).forEach((p) => {
        if (p && p.id) projectNameById[p.id] = p.name || p.id;
    });
}

function rememberProjectsInNameMap(list) {
    (list || []).forEach((p) => {
        if (p && p.id) projectNameById[p.id] = p.name || p.id;
    });
}

/** 与后端 projectListSearchPattern 对齐：name / description / id 子串匹配（忽略大小写） */
function matchProjectSearchQuery(project, query) {
    const q = String(query || '').trim().toLowerCase();
    if (!q) return true;
    const name = String(project.name || '').toLowerCase();
    const desc = String(project.description || '').toLowerCase();
    const id = String(project.id || '').toLowerCase();
    return name.includes(q) || desc.includes(q) || id.includes(q);
}

function sortProjectsForPicker(projects) {
    return [...projects].sort((a, b) => {
        const ap = a.pinned ? 1 : 0;
        const bp = b.pinned ? 1 : 0;
        if (bp !== ap) return bp - ap;
        const au = a.updated_at || a.updatedAt || '';
        const bu = b.updated_at || b.updatedAt || '';
        return String(bu).localeCompare(String(au));
    });
}

/** 从已加载列表中筛选活跃项目（对话选择器 / 项目筛选下拉） */
function filterActiveProjectsLocal(projects, query) {
    const list = (projects || []).filter((p) => p && p.id && p.status !== 'archived');
    const q = String(query || '').trim();
    const filtered = q ? list.filter((p) => matchProjectSearchQuery(p, q)) : list;
    return sortProjectsForPicker(filtered);
}

async function searchActiveProjects(query, opts = {}) {
    const params = new URLSearchParams();
    params.set('status', opts.status || 'active');
    params.set('limit', String(opts.limit ?? (String(query || '').trim() ? PROJECT_PICKER_SEARCH_LIMIT : PROJECT_PICKER_INITIAL_LIMIT)));
    params.set('offset', String(opts.offset ?? 0));
    const q = String(query || '').trim();
    if (q) params.set('search', q);
    const res = await apiFetch(`/api/projects?${params}`);
    if (!res.ok) throw new Error(tp('projects.loadProjectsFailed'));
    const parsed = parseProjectsListResponse(await res.json());
    rememberProjectsInNameMap(parsed.items);
    return parsed;
}

async function fetchProjectSummary(projectId) {
    const id = String(projectId || '').trim();
    if (!id) return null;
    const res = await apiFetch(`/api/projects/${encodeURIComponent(id)}`);
    if (!res.ok) return null;
    const project = await res.json();
    if (project && project.id) rememberProjectsInNameMap([project]);
    return project;
}

function getProjectsListPageSize() {
    try {
        const saved = parseInt(localStorage.getItem(PROJECTS_LIST_PAGE_SIZE_KEY), 10);
        if ([20, 50, 100].includes(saved)) return saved;
    } catch (e) { /* ignore */ }
    return 50;
}

let projectsListPagination = { page: 1, pageSize: getProjectsListPageSize(), total: 0 };
let projectsListSearch = '';
let _projectsListSearchDebounce = null;

function parseListTotalValue(raw, itemsLength) {
    if (typeof raw === 'number' && Number.isFinite(raw) && raw >= 0) return raw;
    if (raw != null && raw !== '') {
        const n = parseInt(String(raw), 10);
        if (Number.isFinite(n) && n >= 0) return n;
    }
    return itemsLength;
}

function parseListOffsetValue(raw) {
    if (typeof raw === 'number' && Number.isFinite(raw) && raw >= 0) return raw;
    if (raw != null && raw !== '') {
        const n = parseInt(String(raw), 10);
        if (Number.isFinite(n) && n >= 0) return n;
    }
    return 0;
}

function parseProjectsListResponse(data) {
    if (Array.isArray(data)) {
        return { items: data, total: data.length, limit: data.length, offset: 0, isLegacyArray: true };
    }
    const items = data.projects || data.items || [];
    const arr = Array.isArray(items) ? items : [];
    return {
        items: arr,
        total: parseListTotalValue(data.total, arr.length),
        limit: parseListTotalValue(data.limit, arr.length) || arr.length,
        offset: parseListOffsetValue(data.offset),
        isLegacyArray: false,
    };
}

async function resolveProjectsListTotal(params, parsed, pageSize, offset) {
    const serverTotal = parsed.total;
    // 服务端 total 明确大于当前页末尾 → 直接信任
    if (!parsed.isLegacyArray && serverTotal > offset + parsed.items.length) {
        return serverTotal;
    }
    // 不足一页 → 已是最后一页
    if (parsed.items.length < pageSize) {
        return Math.max(serverTotal, offset + parsed.items.length);
    }
    // 满页但 total 可能被误算为 items.length → 探测下一页
    const probe = new URLSearchParams(params);
    probe.set('offset', String(offset + pageSize));
    probe.set('limit', '1');
    try {
        const res = await apiFetch(`/api/projects?${probe}`);
        if (!res.ok) return Math.max(serverTotal, offset + parsed.items.length);
        const probeParsed = parseProjectsListResponse(await res.json());
        if (probeParsed.total > serverTotal) return probeParsed.total;
        if (probeParsed.items.length > 0) {
            return Math.max(serverTotal, offset + pageSize + 1);
        }
    } catch (e) { /* ignore */ }
    return Math.max(serverTotal, offset + parsed.items.length);
}

async function fetchAllProjects(includeArchived) {
    const showArchived = includeArchived || document.getElementById('projects-show-archived')?.checked;
    let all = [];
    const pageSize = 200;
    let offset = 0;
    let total = Infinity;
    while (all.length < total) {
        const params = new URLSearchParams({ limit: String(pageSize), offset: String(offset) });
        if (!showArchived) params.set('status', 'active');
        const res = await apiFetch(`/api/projects?${params}`);
        if (!res.ok) throw new Error(tp('projects.loadProjectsFailed'));
        const parsed = parseProjectsListResponse(await res.json());
        all = all.concat(parsed.items);
        total = parsed.total;
        if (!parsed.items.length) break;
        offset += parsed.items.length;
    }
    return all;
}

async function fetchProjectsList(includeArchived, opts = {}) {
    const showArchived = includeArchived || document.getElementById('projects-show-archived')?.checked;
    const page = opts.page ?? projectsListPagination.page;
    const pageSize = opts.pageSize ?? getProjectsListPageSize();
    const search = opts.search !== undefined ? opts.search : projectsListSearch;
    projectsListSearch = search;
    const offset = (page - 1) * pageSize;
    const params = new URLSearchParams({ limit: String(pageSize), offset: String(offset) });
    if (search) params.set('search', search);
    if (!showArchived) params.set('status', 'active');
    const res = await apiFetch(`/api/projects?${params}`);
    if (!res.ok) throw new Error(tp('projects.loadProjectsFailed'));
    const parsed = parseProjectsListResponse(await res.json());
    const total = await resolveProjectsListTotal(params, parsed, pageSize, offset);
    projectsCache = parsed.items;
    projectsListPagination = { page, pageSize: pageSize, total };
    rebuildProjectNameMap(projectsCacheAll.length ? projectsCacheAll : projectsCache);
    return projectsCache;
}

/** 对话页等项目选择器：确保全量列表已拉取（去重并发请求） */
async function ensureProjectsLoaded(force) {
    if (!force && _projectsListReady) return projectsCacheAll;
    if (!force && _projectsFetchPromise) return _projectsFetchPromise;
    _projectsFetchPromise = fetchAllProjects(false)
        .then((list) => {
            projectsCacheAll = list;
            rebuildProjectNameMap(projectsCacheAll);
            _projectsListReady = true;
            if (typeof window.refreshConversationProjectFilter === 'function') {
                window.refreshConversationProjectFilter();
            }
            return projectsCacheAll;
        })
        .catch((e) => {
            _projectsListReady = false;
            throw e;
        })
        .finally(() => {
            _projectsFetchPromise = null;
        });
    return _projectsFetchPromise;
}

function isProjectsCacheReady() {
    return _projectsListReady;
}

function prefetchProjectsForChat() {
    const id = (resolveChatProjectSelection() || '').trim();
    if (id && !projectNameById[id]) {
        fetchProjectSummary(id).catch(() => {});
    }
    ensureProjectsLoaded().catch(() => {});
}

/** 新对话沿用用户最近选择的项目；没有选择时才保持未绑定。 */
async function ensureDefaultActiveProjectForNewChat() {
    const id = getActiveProjectId();
    if (!id) return '';
    const project = await fetchProjectSummary(id).catch(() => null);
    if (project && project.id && project.status !== 'archived') return project.id;
    setActiveProjectId('');
    return '';
}

function getProjectName(id) {
    return projectNameById[id] || id || '';
}

function initProjectsModalEscape() {
    if (window._projectsModalEscapeBound) return;
        document.addEventListener('keydown', (e) => {
        if (e.key !== 'Escape') return;
        if (isProjectsOverlayVisible('project-modal')) closeProjectModal();
    });
}

async function initProjectsPage() {
    const page = document.getElementById('page-projects');
    if (!page || page.style.display === 'none') return;
    initProjectsModalEscape();
    refreshProjectsFilterSelects();
    if (typeof syncAppModalBodyLock === 'function') {
        syncAppModalBodyLock();
    }
    updateProjectsDetailVisibility();
    projectsListPagination.pageSize = getProjectsListPageSize();
    renderProjectsPagination();
    await loadProjectsList();
    if (!currentProjectId && projectsCache.length) {
        const fromHash = new URLSearchParams(window.location.hash.split('?')[1] || '').get('id');
        currentProjectId = fromHash || projectsCache[0].id;
    }
    renderProjectsSidebar();
    if (currentProjectId) {
        await selectProject(currentProjectId);
    }
}

async function loadProjectsList() {
    _projectsListReady = false;
    projectsCacheAll = [];
    projectsListPagination.pageSize = getProjectsListPageSize();
    await fetchProjectsList();
    renderProjectsSidebar();
    renderProjectsPagination();
    try {
        projectsCacheAll = await fetchAllProjects();
        rebuildProjectNameMap(projectsCacheAll);
        _projectsListReady = true;
    } catch (e) {
        console.warn(e);
    }
    if (typeof refreshChatProjectSelector === 'function') {
        refreshChatProjectSelector();
    }
    if (typeof refreshVulnerabilityProjectFilter === 'function') {
        refreshVulnerabilityProjectFilter();
    }
    if (typeof window.refreshAllProjectFilterSelects === 'function') {
        await window.refreshAllProjectFilterSelects();
    }
}

function projectInitial(name) {
    const s = (name || 'P').trim();
    return s ? s.charAt(0).toUpperCase() : 'P';
}

function updateProjectsDetailVisibility() {
    const main = document.getElementById('projects-detail-main');
    const placeholder = document.getElementById('projects-detail-placeholder');
    const inner = document.getElementById('projects-detail-inner');
    const show = !!currentProjectId;
    if (main) main.classList.toggle('has-project', show);
    if (placeholder) placeholder.hidden = show;
    if (inner) inner.hidden = !show;
}

function updateProjectsListCount() {
    const el = document.getElementById('projects-list-count');
    if (el) el.textContent = String(projectsListPagination.total || projectsCache.length);
}


function formatCategoryBadge(category) {
    const raw = (category || '').trim();
    const c = raw.toLowerCase() || 'note';
    const cls = FACT_CATEGORY_BADGE[c] || 'projects-category--custom';
    return `<span class="projects-category ${cls}">${escapeHtml(raw || '—')}</span>`;
}

function formatConfidenceBadge(confidence) {
    const c = (confidence || '').toLowerCase();
    let cls = 'projects-confidence--tentative';
    let label = c || '—';
    if (c === 'confirmed') {
        cls = 'projects-confidence--confirmed';
        label = tp('projects.confidenceConfirmed');
    } else if (c === 'deprecated') {
        cls = 'projects-confidence--deprecated';
        label = tp('projects.confidenceDeprecated');
    } else if (c === 'tentative') {
        label = tp('projects.confidenceTentative');
    }
    return `<span class="projects-confidence ${cls}">${escapeHtml(label)}</span>`;
}


function formatSeverityBadge(severity) {
    const s = (severity || 'info').toLowerCase();
    const cls = 'projects-severity--' + (['critical', 'high', 'medium', 'low', 'info'].includes(s) ? s : 'info');
    return `<span class="projects-severity ${cls}">${escapeHtml(severity || '—')}</span>`;
}

function formatVulnStatusBadge(status) {
    const s = (status || 'open').toLowerCase();
    const labelMap = {
        open: 'vulnerabilityPage.statusOpen',
        confirmed: 'vulnerabilityPage.statusConfirmed',
        fixed: 'vulnerabilityPage.statusFixed',
        false_positive: 'vulnerabilityPage.statusFalsePositive',
        ignored: 'vulnerabilityPage.statusIgnored',
    };
    const label = labelMap[s] ? tp(labelMap[s]) : status || '—';
    const cls = ['open', 'confirmed', 'fixed', 'false_positive', 'ignored'].includes(s) ? s : 'open';
    return `<span class="status-badge status-${escapeHtml(cls)}">${escapeHtml(label)}</span>`;
}

let _projectVulnsFilterDebounce = null;

function buildProjectVulnsQueryParams() {
    const params = new URLSearchParams();
    params.set('project_id', currentProjectId);
    params.set('limit', '200');
    const search = document.getElementById('project-vulns-search')?.value?.trim();
    const severity = document.getElementById('project-vulns-filter-severity')?.value?.trim();
    const status = document.getElementById('project-vulns-filter-status')?.value?.trim();
    if (search) params.set('q', search);
    if (severity) params.set('severity', severity);
    if (status) params.set('status', status);
    return params;
}

function projectVulnsHasActiveFilter() {
    return !!(
        document.getElementById('project-vulns-search')?.value?.trim() ||
        document.getElementById('project-vulns-filter-severity')?.value ||
        document.getElementById('project-vulns-filter-status')?.value
    );
}

function debouncedLoadProjectVulnerabilities() {
    if (_projectVulnsFilterDebounce) clearTimeout(_projectVulnsFilterDebounce);
    _projectVulnsFilterDebounce = setTimeout(() => {
        _projectVulnsFilterDebounce = null;
        loadProjectVulnerabilities();
    }, 280);
}

function getProjectsListFilter() {
    return (document.getElementById('projects-list-search')?.value || '').trim().toLowerCase();
}

function filterProjectsList() {
    if (_projectsListSearchDebounce) clearTimeout(_projectsListSearchDebounce);
    _projectsListSearchDebounce = setTimeout(() => {
        _projectsListSearchDebounce = null;
        const q = getProjectsListFilter();
        projectsListPagination.page = 1;
        fetchProjectsList(undefined, { page: 1, search: q })
            .then(() => {
                renderProjectsSidebar();
                renderProjectsPagination();
            })
            .catch((e) => console.warn(e));
    }, 280);
}

function goProjectsPage(page) {
    const totalPages = Math.max(1, Math.ceil((projectsListPagination.total || 0) / projectsListPagination.pageSize) || 1);
    const next = Math.min(Math.max(1, page), totalPages);
    if (next === projectsListPagination.page) return;
    fetchProjectsList(undefined, { page: next })
        .then(() => {
            renderProjectsSidebar();
            renderProjectsPagination();
            const listEl = document.getElementById('projects-list');
            if (listEl) listEl.scrollTop = 0;
        })
        .catch((e) => console.warn(e));
}

function changeProjectsPageSize() {
    const sel = document.getElementById('projects-page-size-pagination');
    const newSize = sel ? parseInt(sel.value, 10) : 50;
    if (![20, 50, 100].includes(newSize)) return;
    try {
        localStorage.setItem(PROJECTS_LIST_PAGE_SIZE_KEY, String(newSize));
    } catch (e) { /* ignore */ }
    projectsListPagination.pageSize = newSize;
    projectsListPagination.page = 1;
    fetchProjectsList(undefined, { page: 1, pageSize: newSize })
        .then(() => {
            renderProjectsSidebar();
            renderProjectsPagination();
        })
        .catch((e) => console.warn(e));
}

function renderProjectsPagination() {
    const el = document.getElementById('projects-pagination');
    if (!el) return;
    const { page, pageSize, total } = projectsListPagination;
    const totalPages = Math.max(1, Math.ceil(total / pageSize) || 1);
    const navDisabled = total === 0 || totalPages <= 1;
    el.hidden = false;
    const start = total === 0 ? 0 : (page - 1) * pageSize + 1;
    const end = total === 0 ? 0 : Math.min(page * pageSize, total);
    const infoText = tpFmt('projects.paginationRange', `${start}-${end}/${total}`, { start, end, total });
    const pageText = tpFmt('projects.paginationPage', `${page}/${totalPages}`, { page, total: totalPages });
    el.innerHTML = `
        <div class="sidebar-list-pagination-inner sidebar-list-pagination-inner--compact">
            <span class="pagination-info">${escapeHtml(infoText)}</span>
            <div class="pagination-controls">
                <button type="button" class="btn-icon-pagination" onclick="goProjectsPage(${page - 1})" ${page <= 1 || navDisabled ? 'disabled' : ''} title="${escapeHtml(tp('projects.paginationPrev'))}" aria-label="${escapeHtml(tp('projects.paginationPrev'))}">‹</button>
                <span class="pagination-page">${escapeHtml(pageText)}</span>
                <button type="button" class="btn-icon-pagination" onclick="goProjectsPage(${page + 1})" ${page >= totalPages || navDisabled ? 'disabled' : ''} title="${escapeHtml(tp('projects.paginationNext'))}" aria-label="${escapeHtml(tp('projects.paginationNext'))}">›</button>
            </div>
            <label class="pagination-page-size">
                ${escapeHtml(tp('projects.paginationPerPage'))}
                <select id="projects-page-size-pagination" class="projects-filter-select-native">
                    <option value="20" ${pageSize === 20 ? 'selected' : ''}>20</option>
                    <option value="50" ${pageSize === 50 ? 'selected' : ''}>50</option>
                    <option value="100" ${pageSize === 100 ? 'selected' : ''}>100</option>
                </select>
            </label>
        </div>`;
    refreshProjectsFilterSelects();
}

function renderProjectsSidebar() {
    const el = document.getElementById('projects-list');
    if (!el) return;
    updateProjectsListCount();
    const list = projectsCache;
    if (!projectsCache.length) {
        const createBtn = (typeof hasPermission === 'function' && hasPermission('project:write'))
            ? `<button type="button" class="btn-primary btn-small projects-empty-btn" onclick="showNewProjectModal()">${escapeHtml(tp('projects.newProject'))}</button>`
            : '';
        el.innerHTML = `<div class="projects-empty">${escapeHtml(tp('projects.noProjects'))}${createBtn ? `<br>${createBtn}` : ''}</div>`;
        updateProjectsDetailVisibility();
        renderProjectsPagination();
        return;
    }
    if (!list.length) {
        el.innerHTML = `<div class="projects-empty">${escapeHtml(tp('projects.noMatchingProjects'))}</div>`;
        updateProjectsDetailVisibility();
        renderProjectsPagination();
        return;
    }
    el.innerHTML = list.map((p) => {
        const fullName = p.name || tp('common.untitled');
        const displayName = window.formatProjectNameForDisplay(fullName);
        const active = p.id === currentProjectId ? ' is-active' : '';
        const archived = p.status === 'archived' ? ' is-archived' : '';
        const badges = [
            p.pinned ? `<span class="projects-list-item-badge">${escapeHtml(tp('projects.pinned'))}</span>` : '',
            p.status === 'archived' ? `<span class="projects-list-item-badge">${escapeHtml(tp('projects.archived'))}</span>` : '',
        ].join('');
        return `<div class="projects-list-item${active}${archived}" data-id="${escapeAttr(p.id)}" onclick="selectProject(${escapeJsStringAttr(p.id)})">
            <div class="projects-list-item-body">
                <div class="projects-list-item-name" title="${escapeAttr(fullName)}">${escapeHtml(displayName)}${badges}</div>
                <div class="projects-list-item-meta">${formatProjectTime(p.updated_at)}</div>
            </div>
            <button type="button" class="projects-list-item-menu" title="${escapeHtml(tp('projects.projectActions'))}" aria-label="${escapeHtml(tp('projects.projectActions'))}" onclick="showProjectListActionMenu(event, ${escapeJsStringAttr(p.id)})">⋯</button>
        </div>`;
    }).join('');
    updateProjectsDetailVisibility();
    if (typeof applyRBACToUI === 'function') applyRBACToUI(el);
}

function clampProjectDescription(text) {
    const s = (text || '').trim();
    if (s.length <= PROJECT_DESCRIPTION_MAX_LENGTH) return s;
    return s.slice(0, PROJECT_DESCRIPTION_MAX_LENGTH);
}

function renderProjectDetailTitle(name) {
    const titleEl = document.getElementById('projects-detail-title');
    if (!titleEl) return;
    const text = (name || '').trim() || tp('projects.defaultProjectName');
    window.applyProjectNameDisplay(titleEl, text);
}

function renderProjectDetailDesc(desc) {
    const descEl = document.getElementById('projects-detail-desc');
    if (!descEl) return;
    const text = (desc || '').trim();
    if (!text) {
        descEl.hidden = true;
        descEl.textContent = '';
        descEl.removeAttribute('title');
        return;
    }
    descEl.textContent = text;
    descEl.title = text;
    descEl.hidden = false;
}

function updateProjectStatusPill(status) {
    const el = document.getElementById('projects-detail-status');
    if (!el) return;
    const archived = status === 'archived';
    el.textContent = archived ? tp('projects.statusArchived') : tp('projects.statusActive');
    el.className = 'projects-status-pill ' + (archived ? 'projects-status-pill--archived' : 'projects-status-pill--active');
}

function renderProjectDetailMeta(updatedAt) {
    const metaEl = document.getElementById('projects-detail-meta');
    const timeEl = document.getElementById('projects-detail-meta-time');
    if (!metaEl || !timeEl) return;
    const time = formatProjectTime(updatedAt);
    const full = tpFmt('projects.updatedPrefix', `Updated ${time}`, { time });
    timeEl.textContent = time;
    metaEl.title = full;
}

function refreshProjectDetailMetaI18n() {
    if (!currentProjectId) return;
    let updatedAt = currentProjectUpdatedAt;
    if (updatedAt == null) {
        const source = projectsCacheAll.length ? projectsCacheAll : projectsCache;
        const p = source.find((x) => x.id === currentProjectId);
        updatedAt = p?.updated_at;
    }
    renderProjectDetailMeta(updatedAt);
}

function updateProjectStats(stats) {
    const s = stats || {};
    const v = document.getElementById('project-stat-vulns');
    const c = document.getElementById('project-stat-conversations');
    const vc = s.vuln_count ?? s.vulnCount ?? 0;
    const cc = s.conversation_count ?? s.conversationCount ?? 0;
    if (v) v.textContent = tpFmt('projects.statsVulns', `${vc} vulnerabilities`, { count: vc });
    if (c) c.textContent = tpFmt('projects.statsConversations', `${cc} conversations`, { count: cc });
}

async function selectProject(id) {
    currentProjectId = id;
    if (id) setActiveProjectId(id);
    const vulnSearchEl = document.getElementById('project-vulns-search');
    const vulnSevEl = document.getElementById('project-vulns-filter-severity');
    const vulnStatusEl = document.getElementById('project-vulns-filter-status');
    if (vulnSearchEl) vulnSearchEl.value = '';
    if (vulnSevEl) vulnSevEl.value = '';
    if (vulnStatusEl) vulnStatusEl.value = '';
    syncAllProjectsFilterSelects();
    renderProjectsSidebar();
    updateProjectsDetailVisibility();
    try {
        const res = await apiFetch(`/api/projects/${id}`);
        if (!res.ok) throw new Error(tp('projects.projectNotFound'));
        const p = await res.json();
        renderProjectDetailTitle(p.name);
        document.getElementById('project-edit-name').value = p.name || '';
        document.getElementById('project-edit-description').value = p.description || '';
        document.getElementById('project-edit-scope').value = p.scope_json || '';
        const statusEl = document.getElementById('project-edit-status');
        if (statusEl) statusEl.value = p.status || 'active';
        syncAllProjectsFilterSelects();
        const pinEl = document.getElementById('project-edit-pinned');
        if (pinEl) pinEl.checked = !!p.pinned;
        updateProjectStatusPill(p.status || 'active');
        currentProjectUpdatedAt = p.updated_at;
        renderProjectDetailMeta(currentProjectUpdatedAt);
        renderProjectDetailDesc(p.description);
        projectNameById[p.id] = p.name || p.id;
    } catch (e) {
        console.warn(e);
    }
    await refreshProjectHeaderStats();
    switchProjectTab(currentProjectTab);
}

function switchProjectTab(tab) {
    currentProjectTab = tab;
    ['conversations', 'vulns', 'settings'].forEach((t) => {
        const btn = document.getElementById(`project-tab-${t}`);
        const panel = document.getElementById(`project-panel-${t}`);
        if (btn) btn.classList.toggle('is-active', t === tab);
        if (panel) panel.hidden = t !== tab;
    });
    if (tab === 'conversations') loadProjectConversations();
    if (tab === 'vulns') loadProjectVulnerabilities();
}





























async function refreshProjectHeaderStats() {
    if (!currentProjectId) return;
    try {
        const res = await apiFetch(`/api/projects/${currentProjectId}/stats`);
        if (!res.ok) return;
        const stats = await res.json();
        updateProjectStats(stats);
    } catch (e) {
        console.warn(e);
    }
}

async function loadProjectConversations() {
    const tbody = document.getElementById('project-conversations-tbody');
    if (!tbody || !currentProjectId) return;
    tbody.innerHTML = `<tr class="is-empty-row"><td colspan="3">${escapeHtml(tp('common.loading'))}</td></tr>`;
    const res = await apiFetch(`/api/projects/${currentProjectId}/conversations?limit=100`);
    if (!res.ok) {
        tbody.innerHTML = `<tr class="is-empty-row"><td colspan="3">${escapeHtml(tp('common.loadFailed'))}</td></tr>`;
        return;
    }
    const data = await res.json();
    const items = data.conversations || [];
    if (!items.length) {
        tbody.innerHTML = `<tr class="is-empty-row"><td colspan="3">${escapeHtml(tp('projects.noBoundConversations'))}</td></tr>`;
        return;
    }
    tbody.innerHTML = items
        .map((conv) => {
            const id = conv.id;
            const idEsc = escapeHtml(id);
            const title = escapeHtml(conv.title || tp('projects.untitledConversation'));
            const updated = formatProjectTime(conv.updatedAt || conv.updated_at, conv.createdAt || conv.created_at);
            return `<tr>
            <td class="cell-summary" title="${title}">${title}</td>
            <td>${escapeHtml(updated)}</td>
            <td class="col-actions">
                <div class="projects-table-actions">
                    <button type="button" class="projects-action-btn projects-action-btn--view" data-conv-id="${idEsc}" onclick="openProjectConversation(this.dataset.convId)">${escapeHtml(tp('projects.open'))}</button>
                    <button type="button" class="projects-action-btn projects-action-btn--mute" data-conv-id="${idEsc}" onclick="unbindConversationFromProject(this.dataset.convId)" title="${escapeHtml(tp('projects.unbindProjectTitle'))}">${escapeHtml(tp('projects.unbind'))}</button>
                </div>
            </td>
        </tr>`;
        })
        .join('');
}

function openProjectConversation(conversationId) {
    if (!conversationId) return;
    if (typeof switchPage === 'function') {
        switchPage('chat');
    }
    setTimeout(() => {
        if (typeof loadConversation === 'function') {
            loadConversation(conversationId);
        }
    }, 200);
}


async function unbindConversationFromProject(conversationId) {
    if (!conversationId || !confirm(tp('projects.confirmUnbindConversation'))) return;
    const res = await apiFetch(`/api/conversations/${encodeURIComponent(conversationId)}/project`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ projectId: '' }),
    });
    if (!res.ok) return alert(tp('projects.unbindFailed'));
    loadProjectConversations();
    refreshProjectHeaderStats();
}










function openVulnerabilitiesForProject(projectId) {
    const pid = projectId || currentProjectId;
    if (!pid) return;
    if (typeof switchPage === 'function') {
        switchPage('vulnerabilities');
    }
    if (typeof window.setVulnerabilityProjectFilter === 'function') {
        window.setVulnerabilityProjectFilter(pid);
    } else {
        window.location.hash = `vulnerabilities?project_id=${encodeURIComponent(pid)}`;
    }
}

async function loadProjectVulnerabilities() {
    const tbody = document.getElementById('project-vulns-tbody');
    if (!tbody || !currentProjectId) return;
    tbody.innerHTML = `<tr class="is-empty-row"><td colspan="4">${escapeHtml(tp('common.loading'))}</td></tr>`;
    const qs = buildProjectVulnsQueryParams().toString();
    const res = await apiFetch(`/api/vulnerabilities?${qs}`);
    if (!res.ok) {
        tbody.innerHTML = `<tr class="is-empty-row"><td colspan="4">${escapeHtml(tp('common.loadFailed'))}</td></tr>`;
        return;
    }
    const data = await res.json();
    const items = data.Vulnerabilities || data.vulnerabilities || data.items || (Array.isArray(data) ? data : []);
    if (!items.length) {
        tbody.innerHTML = `<tr class="is-empty-row"><td colspan="4">${
            projectVulnsHasActiveFilter() ? tp('projects.noMatchingVulns') : tp('projects.noVulnerabilityRecords')
        }</td></tr>`;
        refreshProjectHeaderStats();
        return;
    }
    tbody.innerHTML = items.map((v) => {
        const idEsc = escapeHtml(v.id);
        return `<tr>
            <td class="cell-summary" title="${escapeHtml(v.title)}">${escapeHtml(v.title)}</td>
            <td>${formatSeverityBadge(v.severity)}</td>
            <td>${formatVulnStatusBadge(v.status)}</td>
            <td class="col-actions">
                <div class="projects-table-actions">
                    <button type="button" class="projects-action-btn projects-action-btn--view" data-vuln-id="${idEsc}" onclick="openVulnerabilityDetail(this.dataset.vulnId)">${escapeHtml(tp('common.view'))}</button>
                </div>
            </td>
        </tr>`;
    }).join('');
    refreshProjectHeaderStats();
}

function openVulnerabilityDetail(vulnId) {
    openVulnerabilitiesForProject(currentProjectId);
    if (typeof window.setVulnerabilityIdFilter === 'function') {
        setTimeout(() => window.setVulnerabilityIdFilter(vulnId), 300);
    }
}


function openProjectsOverlay(id, opts) {
    openAppModal(id, opts);
}

function isProjectsOverlayVisible(id) {
    return isAppModalOpen(id);
}

function closeProjectsOverlay(id) {
    closeAppModal(id);
}

function showNewProjectModal() {
    if (!requireProjectWrite()) return;
    document.getElementById('project-modal-title').textContent = tp('projects.modalNewTitle');
    const sub = document.getElementById('project-modal-subtitle');
    if (sub) sub.textContent = tp('projects.modalNewSubtitle');
    const submitBtn = document.getElementById('project-modal-submit-btn');
    if (submitBtn) submitBtn.textContent = tp('projects.createProject');
    document.getElementById('project-modal-name').value = '';
    document.getElementById('project-modal-description').value = '';
        openProjectsOverlay('project-modal');
}

async function showEditProjectModal(projectId, options = {}) {
    if (!projectId) return;
    if (!requireProjectWrite()) return;
        window._projectModalFromChatSidebar = options.fromChatSidebar === true;
    window._projectModalEditId = projectId;
    document.getElementById('project-modal-title').textContent = tp('projects.modalEditTitle');
    const sub = document.getElementById('project-modal-subtitle');
    if (sub) sub.textContent = tp('projects.modalEditSubtitle');
    const submitBtn = document.getElementById('project-modal-submit-btn');
    if (submitBtn) submitBtn.textContent = tp('projects.saveChanges');
    const nameEl = document.getElementById('project-modal-name');
    const descEl = document.getElementById('project-modal-description');
    if (nameEl) nameEl.value = '';
    if (descEl) descEl.value = '';
    openProjectsOverlay('project-modal', { focus: false });
    let p = findProjectById(projectId);
    if (!p) {
        try {
            const res = await apiFetch(`/api/projects/${encodeURIComponent(projectId)}`);
            if (!res.ok) throw new Error(tp('projects.projectNotFound'));
            p = await res.json();
        } catch (e) {
            closeProjectModal();
            alert(e.message || tp('projects.projectNotFound'));
                        return;
        }
    }
    const name = (p.name || '').slice(0, PROJECT_NAME_MAX_LENGTH);
    const description = clampProjectDescription(p.description || '');
    deferModalContent(() => {
        if (nameEl) nameEl.value = name;
        if (descEl) descEl.value = description;
        nameEl?.focus();
    });
}

/** 从对话区「选择项目」面板打开新建项目，创建成功后自动绑定当前对话 */
function showNewProjectModalFromChat() {
    closeChatProjectPanel();
        showNewProjectModal();
}

/** 从对话侧栏新建项目，保持当前对话的项目绑定不变。 */
function showNewProjectModalFromChatSidebar() {
    if (!requireProjectWrite()) return;
            showNewProjectModal();
}

async function saveProjectModal() {
    if (!requireProjectWrite()) return;
    const name = document.getElementById('project-modal-name').value.trim().slice(0, PROJECT_NAME_MAX_LENGTH);
    if (!name) return alert(tp('projects.enterProjectName'));
    const body = {
        name,
        description: clampProjectDescription(document.getElementById('project-modal-description').value),
    };
    const editId = window._projectModalEditId;
    const submitBtn = document.getElementById('project-modal-submit-btn');
    if (submitBtn) submitBtn.disabled = true;
    try {
        const res = editId
            ? await apiFetch(`/api/projects/${editId}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
            : await apiFetch('/api/projects', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
        if (!(await notifyProjectApiFailure(res, 'projects.saveFailed', '保存失败'))) return;
        const fromChat = !!window._projectModalFromChat;
        const fromChatSidebar = !!window._projectModalFromChatSidebar;
        const fromWebshellConnId = window._projectModalFromWebshellConnId || '';
                        window._projectModalFromWebshellConnId = '';
        closeProjectModal();
        const saved = await res.json();
        await loadProjectsList();
        if (saved.id) {
            if (fromWebshellConnId && !editId) {
                if (typeof applyWebshellAiProjectSelection === 'function') {
                    await applyWebshellAiProjectSelection(saved.id);
                }
            } else if (fromChat && !editId) {
                await applyChatProjectSelection(saved.id);
            } else if (!fromChatSidebar) {
                await selectProject(saved.id);
            }
        }
    } catch (error) {
        if (typeof notifyApiError === 'function') {
            notifyApiError(error?.message || tpFmt('projects.saveFailed', '保存失败'));
        } else {
            alert(error?.message || tpFmt('projects.saveFailed', '保存失败'));
        }
    } finally {
        if (submitBtn) submitBtn.disabled = false;
    }
}

function closeProjectModal() {
                closeProjectsOverlay('project-modal');
}

function formatProjectScopeJson() {
    const el = document.getElementById('project-edit-scope');
    if (!el) return;
    const raw = el.value.trim();
    if (!raw) return;
    try {
        el.value = JSON.stringify(JSON.parse(raw), null, 2);
    } catch (e) {
        alert(tp('projects.invalidJson') + ': ' + (e.message || String(e)));
    }
}

function insertProjectScopeExample() {
    const el = document.getElementById('project-edit-scope');
    if (!el) return;
    const example = {
        targets: ['https://example.com'],
        exclude: ['*.cdn.example.com'],
        notes: tp('projects.scopeNoteAuthorizedWebOnly'),
    };
    el.value = JSON.stringify(example, null, 2);
    el.focus();
}

async function saveProjectSettings() {
    if (!currentProjectId || !requireProjectWrite()) return;
    const scopeRaw = document.getElementById('project-edit-scope').value.trim();
    if (scopeRaw) {
        try {
            JSON.parse(scopeRaw);
        } catch (e) {
            alert(tp('projects.invalidScopeJson') + ': ' + (e.message || String(e)));
            return;
        }
    }
    const body = {
        name: document.getElementById('project-edit-name').value.trim(),
        description: clampProjectDescription(document.getElementById('project-edit-description').value),
        scope_json: scopeRaw,
        status: document.getElementById('project-edit-status')?.value || 'active',
        pinned: !!document.getElementById('project-edit-pinned')?.checked,
    };
    const res = await apiFetch(`/api/projects/${currentProjectId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
    });
    if (!(await notifyProjectApiFailure(res, 'projects.saveFailed', '保存失败'))) return;
    await loadProjectsList();
    await selectProject(currentProjectId);
    if (typeof notifyApiError === 'function') {
        notifyApiError(tp('projects.saved'), 'success');
    } else {
        alert(tp('projects.saved'));
    }
}

function findProjectById(projectId) {
    return projectsCache.find((p) => p.id === projectId) || projectsCacheAll.find((p) => p.id === projectId);
}

let _projectListMenuTargetId = null;
let _projectListMenuSource = '';
let _projectListMenuDocClickBound = false;

function closeProjectListActionMenu() {
    const menu = document.getElementById('projects-list-action-menu');
    if (!menu) return;
    menu.style.display = 'none';
    _projectListMenuTargetId = null;
    _projectListMenuSource = '';
}

function positionProjectListActionMenu(event) {
    const menu = document.getElementById('projects-list-action-menu');
    if (!menu) return;
    menu.style.display = 'block';
    menu.style.visibility = 'visible';
    menu.style.opacity = '1';
    void menu.offsetHeight;
    const menuRect = menu.getBoundingClientRect();
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    let left = event.clientX;
    let top = event.clientY;
    if (left + menuRect.width > viewportWidth) {
        left = Math.max(8, event.clientX - menuRect.width);
    }
    if (top + menuRect.height > viewportHeight) {
        top = Math.max(8, event.clientY - menuRect.height);
    }
    menu.style.left = `${left}px`;
    menu.style.top = `${top}px`;
}

function showProjectListActionMenu(event, projectId, source = '') {
    event.stopPropagation();
    event.preventDefault();
    const menu = document.getElementById('projects-list-action-menu');
    if (!menu) return;
    if (_projectListMenuTargetId === projectId && menu.style.display === 'block') {
        closeProjectListActionMenu();
        return;
    }
    closeProjectListActionMenu();
    const p = findProjectById(projectId);
    if (!p) return;
    _projectListMenuTargetId = projectId;
    _projectListMenuSource = source;
    const editText = document.getElementById('projects-list-menu-edit-text');
    const pinText = document.getElementById('projects-list-menu-pin-text');
    const archiveText = document.getElementById('projects-list-menu-archive-text');
    const deleteText = document.getElementById('projects-list-menu-delete-text');
    if (editText) {
        editText.textContent = source === 'chat'
            ? pickerMessage(tp, 'projects.renameProject', '重命名')
            : tp('projects.editProject');
    }
    if (pinText) {
        pinText.textContent = p.pinned
            ? pickerMessage(tp, 'projects.unpinProjectAction', '取消置顶')
            : pickerMessage(tp, 'projects.pinProjectAction', '置顶项目');
    }
    if (archiveText) {
        archiveText.textContent = p.status === 'archived'
            ? tp('projects.restoreProjectActive')
            : tp('projects.archiveProject');
    }
    if (deleteText) deleteText.textContent = tp('projects.deleteProject');
    positionProjectListActionMenu(event);
    if (typeof applyRBACToUI === 'function') applyRBACToUI(menu);
}

function updateCachedProjectPinnedState(projectId, pinned) {
    const update = (project) => {
        if (project?.id === projectId) project.pinned = !!pinned;
        return project;
    };
    projectsCache = sortProjectsForPicker(projectsCache.map(update));
    projectsCacheAll = sortProjectsForPicker(projectsCacheAll.map(update));
    renderProjectsSidebar();
    if (isProjectsCacheReady() && chatProjectFolderContext.ready) {
        renderChatProjectFolders(projectsCacheAll);
    }
}

async function toggleProjectPinnedFromListMenu() {
    if (!requireProjectWrite()) return;
    const projectId = _projectListMenuTargetId;
    const project = findProjectById(projectId);
    closeProjectListActionMenu();
    if (!projectId || !project) return;

    const nextPinned = !project.pinned;
    const res = await apiFetch(`/api/projects/${projectId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ pinned: nextPinned }),
    });
    if (!(await notifyProjectApiFailure(res, 'projects.operationFailed', '操作失败'))) return;

    updateCachedProjectPinnedState(projectId, nextPinned);
    await loadProjectsList();
}

function initProjectListActionMenu() {
    if (_projectListMenuDocClickBound) return;
    _projectListMenuDocClickBound = true;
    document.addEventListener('click', (event) => {
        const menu = document.getElementById('projects-list-action-menu');
        if (!menu || menu.style.display === 'none') return;
        if (menu.contains(event.target)) return;
        if (event.target.closest('.projects-list-item-menu, .project-folder-menu')) return;
        closeProjectListActionMenu();
    });
    document.addEventListener('keydown', (event) => {
        if (event.key === 'Escape') closeProjectListActionMenu();
    });
}

async function toggleProjectArchiveById(projectId) {
    if (!requireProjectWrite()) return;
    const p = findProjectById(projectId);
    if (!p) return;
    const cur = p.status || 'active';
    const next = cur === 'archived' ? 'active' : 'archived';
    if (!confirm(next === 'archived' ? tp('projects.confirmArchiveProject') : tp('projects.confirmRestoreProjectActive'))) return;
    const res = await apiFetch(`/api/projects/${projectId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ status: next }),
    });
    if (!(await notifyProjectApiFailure(res, 'projects.operationFailed', '操作失败'))) return;
    await loadProjectsList();
    if (currentProjectId === projectId && projectsCache.some((item) => item.id === projectId)) {
        await selectProject(projectId);
    } else if (currentProjectId === projectId) {
        currentProjectId = null;
        updateProjectsDetailVisibility();
        if (projectsCache.length) await selectProject(projectsCache[0].id);
    }
}

async function deleteProjectById(projectId) {
    if (!requireProjectDelete()) return;
    if (!projectId || !confirm(tp('projects.confirmDeleteProject'))) return;
    const deletedIndex = projectsCache.findIndex((p) => p.id === projectId);
    const res = await apiFetch(`/api/projects/${projectId}`, { method: 'DELETE' });
    if (!(await notifyProjectApiFailure(res, 'projects.deleteFailed', '删除失败'))) return;
    if (getActiveProjectId() === projectId) setActiveProjectId('');
    if (currentProjectId === projectId) currentProjectId = null;
    await loadProjectsList();
    if (projectsCache.length) {
        const nextIndex = Math.min(deletedIndex >= 0 ? deletedIndex : 0, projectsCache.length - 1);
        await selectProject(projectsCache[nextIndex].id);
    } else {
        updateProjectsDetailVisibility();
    }
}

async function toggleProjectArchiveFromListMenu() {
    const projectId = _projectListMenuTargetId;
    closeProjectListActionMenu();
    if (!projectId) return;
    await toggleProjectArchiveById(projectId);
}

function editProjectFromListMenu() {
    const projectId = _projectListMenuTargetId;
    const fromChatSidebar = _projectListMenuSource === 'chat';
    closeProjectListActionMenu();
    if (!projectId) return;
    showEditProjectModal(projectId, { fromChatSidebar });
}

async function deleteProjectFromListMenu() {
    const projectId = _projectListMenuTargetId;
    closeProjectListActionMenu();
    if (!projectId) return;
    await deleteProjectById(projectId);
}

async function archiveCurrentProject() {
    if (!currentProjectId) return;
    await toggleProjectArchiveById(currentProjectId);
}

async function deleteCurrentProject() {
    if (!currentProjectId) return;
    await deleteProjectById(currentProjectId);
}








function parseProjectDate(t) {
    if (t == null || t === '') return null;
    if (typeof t === 'number' && Number.isFinite(t)) {
        const d = new Date(t);
        return isNaN(d.getTime()) || d.getFullYear() < 2000 ? null : d;
    }
    let s = String(t).trim();
    if (!s || s.startsWith('0001-01-01')) return null;
    let d = new Date(s);
    if (!isNaN(d.getTime()) && d.getFullYear() >= 2000) return d;
    const m = s.match(
        /^(\d{4})-(\d{2})-(\d{2})[T\s](\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?(?:([Zz]|([+-])(\d{2}):?(\d{2}))?)?$/,
    );
    if (m) {
        const ms = m[7] ? parseInt(String(m[7]).slice(0, 3).padEnd(3, '0'), 10) : 0;
        let offMin = 0;
        if (m[8] && m[9] && m[10]) {
            offMin = parseInt(m[10], 10) * 60 + parseInt(m[11] || '0', 10);
            if (m[9] === '-') offMin = -offMin;
        }
        d = new Date(
            Date.UTC(
                parseInt(m[1], 10),
                parseInt(m[2], 10) - 1,
                parseInt(m[3], 10),
                parseInt(m[4], 10),
                parseInt(m[5], 10),
                parseInt(m[6], 10),
                ms,
            ) - offMin * 60 * 1000,
        );
        if (!isNaN(d.getTime()) && d.getFullYear() >= 2000) return d;
    }
    return null;
}

function formatProjectTime(t, fallback) {
    const d = parseProjectDate(t) || (fallback != null ? parseProjectDate(fallback) : null);
    if (!d) return tp('projects.notUpdatedYet');
    const now = Date.now();
    const diff = now - d.getTime();
    if (diff < 60000) return tp('common.justNow');
    if (diff < 3600000) return tp('common.minutesAgo', { n: Math.floor(diff / 60000) });
    if (diff < 86400000) return tp('common.hoursAgo', { n: Math.floor(diff / 3600000) });
    if (diff < 604800000) return tp('common.daysAgo', { n: Math.floor(diff / 86400000) });
    return d.toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

function escapeHtml(s) {
    if (s == null) return '';
    return String(s)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');
}

function escapeAttr(s) {
    return escapeHtml(s).replace(/'/g, '&#39;');
}

function escapeJsString(text) {
    return JSON.stringify(String(text == null ? '' : text));
}

function escapeJsStringAttr(text) {
    return escapeAttr(escapeJsString(text));
}

function getChatProjectSelection() {
    const convId = window.currentConversationId;
    if (convId) {
        return window._loadedConversationProjectId || '';
    }
    return getActiveProjectId();
}

/** 用于 UI：返回当前选中的项目 ID（有效性由 normalizeStaleChatProjectSelection 异步校验） */
function resolveChatProjectSelection() {
    return getChatProjectSelection() || '';
}

let _normalizingStaleProject = false;

/** 清除 localStorage 或对话上残留的失效项目 ID */
async function normalizeStaleChatProjectSelection() {
    if (_normalizingStaleProject) return;
    const raw = (getChatProjectSelection() || '').trim();
    if (!raw) return;
    const project = await fetchProjectSummary(raw);
    if (project && project.id && project.status !== 'archived') return;

    _normalizingStaleProject = true;
    try {
        if (window.currentConversationId) {
            window._loadedConversationProjectId = '';
            try {
                const res = await apiFetch(
                    `/api/conversations/${encodeURIComponent(window.currentConversationId)}/project`,
                    {
                        method: 'PUT',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ projectId: '' }),
                    }
                );
                if (!res.ok) console.warn(tp('projects.clearStaleProjectBindingFailed'));
            } catch (e) {
                console.warn(e);
            }
        } else {
            setActiveProjectId('');
        }
    } finally {
        _normalizingStaleProject = false;
    }
}

const PROJECT_PICKER_DEBOUNCE_MS = 100;
const projectPickerPanelState = {
    chat: { seq: 0, timer: null },
    webshell: { seq: 0, timer: null },
};

let chatProjectFolderSearchQuery = '';
let chatProjectFolderRenderSeq = 0;
let chatProjectFolderContextLoadSeq = 0;
const CHAT_PROJECT_FOLDER_PAGE_SIZE = 6;
let chatProjectFolderVisibleCount = CHAT_PROJECT_FOLDER_PAGE_SIZE;
let chatProjectFolderLastQuery = '';
const chatProjectFolderExpandedIds = new Set();
let chatProjectFolderLastSelectionId = null;
const CHAT_UNASSIGNED_PROJECT_FOLDER_ID = '__chat_unassigned_project__';
const PROJECT_FOLDER_COMPLETION_SEEN_KEY = 'cyberstrike-project-folder-completion-seen';
const chatProjectFolderContext = {
    ready: false,
    conversations: [],
    runningIds: new Set(),
    completedByConversation: new Map(),
    pendingApprovalByConversation: new Map(),
};
const PROJECT_FOLDER_PREVIEW_OPEN_DELAY_MS = 160;
const PROJECT_FOLDER_PREVIEW_CLOSE_DELAY_MS = 120;
const PROJECT_APPROVAL_TICK_INTERVAL_MS = 1000;
const projectApprovalTickerEntries = new Set();
let projectApprovalTickerId = 0;
let projectFolderPreviewOpenTimer = null;
let projectFolderPreviewCloseTimer = null;
let projectFolderPreviewAnchor = null;
let projectConversationPreviewOpenTimer = null;
let projectConversationPreviewCloseTimer = null;
let projectConversationPreviewAnchor = null;
let projectConversationPreviewSuppressedUntil = 0;

function readProjectFolderCompletionSeen() {
    try {
        const parsed = JSON.parse(localStorage.getItem(PROJECT_FOLDER_COMPLETION_SEEN_KEY) || '{}');
        return parsed && typeof parsed === 'object' ? parsed : {};
    } catch (e) {
        return {};
    }
}

function markProjectConversationViewed(conversationId, completedAt) {
    const id = String(conversationId || '').trim();
    if (!id) return;
    const seen = readProjectFolderCompletionSeen();
    const timestamp = completedAt || new Date().toISOString();
    seen[id] = timestamp;
    try {
        localStorage.setItem(PROJECT_FOLDER_COMPLETION_SEEN_KEY, JSON.stringify(seen));
    } catch (e) { /* ignore */ }
}

function markCurrentProjectConversationViewed() {
    const conversationId = String(window.currentConversationId || '').trim();
    if (!conversationId || !isProjectConversationUnread(conversationId)) return false;
    const completed = chatProjectFolderContext.completedByConversation.get(conversationId);
    markProjectConversationViewed(conversationId, completed?.completedAt);
    if (isProjectsCacheReady() && chatProjectFolderContext.ready) {
        renderChatProjectFolders(projectsCacheAll);
    }
    return true;
}

function initProjectConversationReadTracking() {
    if (window._projectConversationReadTrackingInited) return;
    const chatContainer = document.querySelector('#page-chat .chat-container');
    if (!chatContainer) return;
        const markViewed = () => markCurrentProjectConversationViewed();
    chatContainer.addEventListener('pointerdown', markViewed, { passive: true });
    chatContainer.addEventListener('focusin', markViewed);
    document.getElementById('chat-input')?.addEventListener('input', markViewed);
}

function isProjectConversationUnread(conversationId) {
    const completed = chatProjectFolderContext.completedByConversation.get(conversationId);
    if (!completed || String(completed.status || '').toLowerCase() !== 'completed') return false;
    const completedAt = Date.parse(completed.completedAt || '');
    if (!Number.isFinite(completedAt)) return false;
    const seenAt = Date.parse(readProjectFolderCompletionSeen()[conversationId] || '');
    return !Number.isFinite(seenAt) || completedAt > seenAt;
}

function getProjectApprovalTiming(details) {
    if (!details || typeof details !== 'object') return { timeoutSeconds: 0, expiresAt: 0 };
    let payload = details.payload;
    if (typeof payload === 'string') {
        try { payload = JSON.parse(payload); } catch (e) { payload = {}; }
    }
    if (!payload || typeof payload !== 'object') payload = {};
    const approval = payload.hitlApproval && typeof payload.hitlApproval === 'object' ? payload.hitlApproval : {};
    const timeout = Number(details.timeoutSeconds != null ? details.timeoutSeconds : approval.timeoutSeconds);
    const timeoutSeconds = Number.isFinite(timeout) && timeout > 0 ? Math.floor(timeout) : 0;
    const createdAt = Date.parse(details.createdAt || approval.createdAt || '');
    let expiresAt = Date.parse(details.expiresAt || approval.expiresAt || '');
    if (!Number.isFinite(expiresAt) && timeoutSeconds > 0 && Number.isFinite(createdAt)) {
        expiresAt = createdAt + timeoutSeconds * 1000;
    }
    return { timeoutSeconds, expiresAt: Number.isFinite(expiresAt) ? expiresAt : 0 };
}

function formatProjectApprovalRemaining(milliseconds) {
    const seconds = Math.max(0, Math.ceil(milliseconds / 1000));
    const minutes = Math.floor(seconds / 60);
    return minutes + ':' + String(seconds % 60).padStart(2, '0');
}

function registerProjectApprovalTicker(status, update) {
    const entry = { status, update };
    if (update() === false) return;
    projectApprovalTickerEntries.add(entry);
    if (projectApprovalTickerId) return;
    projectApprovalTickerId = window.setInterval(() => {
        projectApprovalTickerEntries.forEach((candidate) => {
            if (!candidate.status.isConnected || candidate.update() === false) {
                projectApprovalTickerEntries.delete(candidate);
            }
        });
        if (!projectApprovalTickerEntries.size) {
            window.clearInterval(projectApprovalTickerId);
            projectApprovalTickerId = 0;
        }
    }, PROJECT_APPROVAL_TICK_INTERVAL_MS);
}

function bindProjectApprovalProgress(status, details) {
    const timing = getProjectApprovalTiming(details);
    if (!timing.timeoutSeconds || !timing.expiresAt) return;
    const time = status.querySelector('.project-approval-time');
    const value = status.querySelector('.project-approval-progress-value');
    const update = () => {
        const remaining = Math.max(0, timing.expiresAt - Date.now());
        const percent = Math.max(0, Math.min(100, remaining / (timing.timeoutSeconds * 1000) * 100));
        if (time) time.textContent = formatProjectApprovalRemaining(remaining);
        if (value) value.style.width = `${percent.toFixed(2)}%`;
        status.setAttribute('aria-valuenow', String(Math.round(percent)));
        if (remaining <= 0) {
            status.classList.add('is-expired');
            return false;
        }
        return true;
    };
    status.setAttribute('role', 'progressbar');
    status.setAttribute('aria-valuemin', '0');
    status.setAttribute('aria-valuemax', '100');
    registerProjectApprovalTicker(status, update);
}

const PROJECT_APPROVAL_URGENCY_CLASSES = [
    'is-urgency-normal',
    'is-urgency-warning',
    'is-urgency-urgent',
    'is-urgency-critical',
];

function projectApprovalUrgencyLevel(remainingMilliseconds, hasDeadline) {
    if (!hasDeadline) return 'normal';
    const remaining = Math.max(0, Number(remainingMilliseconds) || 0);
    if (remaining <= 60 * 1000) return 'critical';
    if (remaining <= 3 * 60 * 1000) return 'warning';
    return 'normal';
}

function getProjectApprovalUrgency(details) {
    const timing = getProjectApprovalTiming(details);
    if (!timing.timeoutSeconds || !timing.expiresAt) {
        return {
            level: 'normal',
            label: pickerMessage(tp, 'hitl.approvalUrgencyUnlimited', '审批不限时'),
            remaining: 0,
        };
    }
    const remaining = Math.max(0, timing.expiresAt - Date.now());
    const level = projectApprovalUrgencyLevel(remaining, true);
    const urgencyLabels = {
        critical: pickerMessage(tp, 'hitl.approvalUrgencyWithinOne', '最早审批将在 1 分钟内到期'),
        warning: pickerMessage(tp, 'hitl.approvalUrgencyOneToThree', '最早审批将在 1–3 分钟内到期'),
        normal: pickerMessage(tp, 'hitl.approvalUrgencyMoreThanThree', '最早审批将在 3 分钟后到期'),
    };
    return {
        level,
        label: urgencyLabels[level],
        remaining,
    };
}

function bindProjectApprovalUrgency(status, details, baseLabel) {
    const timing = getProjectApprovalTiming(details);
    const update = () => {
        const urgency = getProjectApprovalUrgency(details);
        status.classList.remove(...PROJECT_APPROVAL_URGENCY_CLASSES);
        status.classList.add(`is-urgency-${urgency.level}`);
        status.dataset.approvalUrgency = urgency.level;
        status.setAttribute('aria-label', `${baseLabel}，${urgency.label}`);
        status.title = `${baseLabel} · ${urgency.label}`;
        return !(timing.expiresAt && urgency.remaining <= 0);
    };
    if (timing.timeoutSeconds && timing.expiresAt) {
        registerProjectApprovalTicker(status, update);
    } else {
        update();
    }
}

function createProjectTaskStatus(kind, details, options) {
    if (!kind) return null;
    const config = options && typeof options === 'object' ? options : {};
    const isApprovalSummary = kind === 'approval' && config.aggregate === true;
    const approvalCount = Math.max(0, Math.floor(Number(config.count) || 0));
    const status = document.createElement('span');
    status.className = `project-task-status project-task-status--${kind}`;
    const label = isApprovalSummary
        ? tpFmt('hitl.waitingApprovalCount', `等待批准 ${approvalCount}`, { count: approvalCount })
        : (kind === 'approval'
        ? pickerMessage(tp, 'hitl.waitingApprovalShort', '等待批准')
        : (kind === 'running'
            ? pickerMessage(tp, 'tasks.statusRunning', '运行中')
            : pickerMessage(tp, 'chat.completedUnread', '已完成，尚未查看')));
    if (kind === 'approval') {
        const timing = getProjectApprovalTiming(details);
        status.innerHTML = '<span class="project-approval-label"></span>' +
            (!isApprovalSummary && timing.timeoutSeconds && timing.expiresAt
                ? '<span class="project-approval-time"></span><span class="project-approval-progress"><span class="project-approval-progress-value"></span></span>'
                : '');
        status.querySelector('.project-approval-label').textContent = label;
        if (isApprovalSummary) {
            // 项目文件夹只汇总待审批数量，始终使用绿色；
            // 紧急程度仅属于具体对话，避免多个审批让项目颜色来回跳变。
            status.classList.add('project-task-status--approval-summary', 'is-urgency-normal');
            status.dataset.approvalCount = String(approvalCount);
            status.dataset.approvalUrgency = 'normal';
            status.setAttribute('aria-label', label);
            status.title = label;
        } else {
            bindProjectApprovalProgress(status, details);
            bindProjectApprovalUrgency(status, details, label);
        }
    }
    if (!isApprovalSummary) {
        status.setAttribute('aria-label', label);
        status.title = label;
    }
    return status;
}

function appendProjectTaskStatuses(container, kinds, detailsByKind, optionsByKind) {
    const normalizedKinds = Array.from(new Set((Array.isArray(kinds) ? kinds : [kinds]).filter(Boolean)));
    if (!container || !normalizedKinds.length) return;
    const group = document.createElement('span');
    group.className = 'project-task-status-group';
    normalizedKinds.forEach((kind) => {
        const status = createProjectTaskStatus(
            kind,
            detailsByKind && detailsByKind[kind],
            optionsByKind && optionsByKind[kind]
        );
        if (status) group.appendChild(status);
    });
    if (!group.childElementCount) return null;
    container.appendChild(group);
    return group;
}

function getProjectFolderStatuses(conversations) {
    const kinds = [];
    if (conversations.some((conversation) => chatProjectFolderContext.pendingApprovalByConversation.has(conversation.id))) {
        kinds.push('approval');
    }
    if (conversations.some((conversation) => isProjectConversationUnread(conversation.id))) {
        kinds.push('unread');
    }
    if (conversations.some((conversation) => chatProjectFolderContext.runningIds.has(conversation.id))) {
        kinds.push('running');
    }
    return kinds;
}

function projectFolderDisclosureMarkup(isExpanded) {
    const path = isExpanded ? 'M3.5 5.5 7.5 9.5l4-4' : 'm5.5 3.5 4 4-4 4';
    return `<svg width="15" height="15" viewBox="0 0 15 15" fill="none" aria-hidden="true"><path d="${path}" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round"/></svg>`;
}

function projectFolderIconMarkup(isExpanded) {
    // Codex 风格：折叠时为闭合文件夹，展开时露出向前翻开的文件夹盖。
    const path = isExpanded
        ? 'M3.5 18V6.5a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2v1.25M3.5 18l1.75-6.25A2 2 0 0 1 7.18 10.3H20.5l-2 7.7H3.5Z'
        : 'M3.5 7a2 2 0 0 1 2-2h4l2 2H18.5a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V7Zm0 2.5h17';
    return `<svg width="19" height="19" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="${path}" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round"/></svg>`;
}

function clampProjectPreviewText(value, maxLength = 220) {
    const text = String(value || '').replace(/\s+/g, ' ').trim();
    return text.length > maxLength ? `${text.slice(0, maxLength - 1)}…` : text;
}

function getProjectScopePreview(project) {
    const raw = String(project?.scope_json || project?.scopeJson || '').trim();
    if (!raw) return '';
    try {
        const parsed = JSON.parse(raw);
        const values = [];
        const appendValue = (value) => {
            if (typeof value === 'string' || typeof value === 'number') {
                const text = String(value).trim();
                if (text) values.push(text);
            } else if (Array.isArray(value)) {
                value.slice(0, 4).forEach(appendValue);
            }
        };
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
            ['targets', 'target', 'domains', 'ips', 'urls', 'scope'].forEach((key) => appendValue(parsed[key]));
            if (!values.length) appendValue(parsed.notes);
        } else {
            appendValue(parsed);
        }
        return clampProjectPreviewText(values.slice(0, 4).join(' · '));
    } catch (e) {
        return clampProjectPreviewText(raw);
    }
}

function ensureProjectFolderPreview() {
    let preview = document.getElementById('project-folder-preview');
    if (preview) return preview;

    preview = document.createElement('div');
    preview.id = 'project-folder-preview';
    preview.className = 'project-folder-preview';
    preview.hidden = true;
    preview.setAttribute('role', 'dialog');
    preview.setAttribute('aria-label', pickerMessage(tp, 'chat.projectPreviewLabel', '项目信息'));
    preview.innerHTML = `
        <div class="project-folder-preview-header">
            <span class="project-folder-preview-icon" aria-hidden="true">
                <svg width="22" height="22" viewBox="0 0 24 24" fill="none"><path d="M3 6.5h6l2 2h10v9.5a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"/></svg>
            </span>
            <span class="project-folder-preview-title"></span>
        </div>
        <div class="project-folder-preview-stats">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M21 12a8.5 8.5 0 0 1-9 8.48A8.5 8.5 0 1 1 20.48 11H21v5l-2-2" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/></svg>
            <span></span>
        </div>
        <div class="project-folder-preview-details">
            <div class="project-folder-preview-detail project-folder-preview-description">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true"><circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="1.7"/><path d="M12 11v5M12 8h.01" stroke="currentColor" stroke-width="1.9" stroke-linecap="round"/></svg>
                <span></span>
            </div>
            <div class="project-folder-preview-detail project-folder-preview-scope">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true"><circle cx="12" cy="12" r="8" stroke="currentColor" stroke-width="1.7"/><circle cx="12" cy="12" r="3" stroke="currentColor" stroke-width="1.7"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/></svg>
                <span></span>
            </div>
        </div>
        <button type="button" class="project-folder-preview-edit" data-require-permission="project:write">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M12 15.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7Z" stroke="currentColor" stroke-width="1.7"/><path d="M19.4 15a1.7 1.7 0 0 0 .34 1.88l.06.06-2.83 2.83-.06-.06a1.7 1.7 0 0 0-1.88-.34 1.7 1.7 0 0 0-1.03 1.56V21h-4v-.08A1.7 1.7 0 0 0 8.95 19.4a1.7 1.7 0 0 0-1.88.34l-.06.06-2.83-2.83.06-.06A1.7 1.7 0 0 0 4.6 15a1.7 1.7 0 0 0-1.55-1H3v-4h.08A1.7 1.7 0 0 0 4.6 8.95a1.7 1.7 0 0 0-.34-1.88l-.06-.06 2.83-2.83.06.06A1.7 1.7 0 0 0 8.95 4.6 1.7 1.7 0 0 0 10 3.08V3h4v.08a1.7 1.7 0 0 0 1.03 1.55 1.7 1.7 0 0 0 1.88-.34l.06-.06 2.83 2.83-.06.06A1.7 1.7 0 0 0 19.4 9c.14.61.69 1.04 1.32 1.04H21v4h-.28A1.7 1.7 0 0 0 19.4 15Z" stroke="currentColor" stroke-width="1.35" stroke-linecap="round" stroke-linejoin="round"/></svg>
            <span></span>
        </button>
    `;
    document.body.appendChild(preview);

    preview.addEventListener('mouseenter', () => {
        clearTimeout(projectFolderPreviewCloseTimer);
    });
    preview.addEventListener('mouseleave', scheduleHideProjectFolderPreview);
    preview.querySelector('.project-folder-preview-edit')?.addEventListener('click', () => {
        const projectId = preview.dataset.projectId || '';
        hideProjectFolderPreview(true);
        if (projectId) showEditProjectModal(projectId, { fromChatSidebar: true });
    });
    window.addEventListener('resize', () => hideProjectFolderPreview(true));
    window.addEventListener('scroll', () => hideProjectFolderPreview(true), true);
    document.addEventListener('keydown', (event) => {
        if (event.key === 'Escape' && !preview.hidden) hideProjectFolderPreview(true);
    });
    return preview;
}

function positionProjectFolderPreview(preview, row) {
    if (!preview || !row?.isConnected) return;
    const anchorRect = row.getBoundingClientRect();
    const previewRect = preview.getBoundingClientRect();
    const gap = 10;
    const edge = 12;
    let left = anchorRect.right + gap;
    if (left + previewRect.width > window.innerWidth - edge) {
        left = Math.max(edge, anchorRect.left - previewRect.width - gap);
    }
    const top = Math.min(
        Math.max(edge, anchorRect.top - 10),
        Math.max(edge, window.innerHeight - previewRect.height - edge)
    );
    preview.style.left = `${Math.round(left)}px`;
    preview.style.top = `${Math.round(top)}px`;
}

function showProjectFolderPreview(project, row, conversations) {
    if (!project || !row?.isConnected || window.matchMedia('(max-width: 900px), (hover: none)').matches) return;
    hideProjectConversationPreview(true);
    const preview = ensureProjectFolderPreview();
    const isUnassigned = project._isUnassigned === true;
    clearTimeout(projectFolderPreviewCloseTimer);
    if (projectFolderPreviewAnchor && projectFolderPreviewAnchor !== row) {
        projectFolderPreviewAnchor.querySelector('.project-folder-item')?.removeAttribute('aria-controls');
    }
    projectFolderPreviewAnchor = row;
    const title = project.name || tp('common.untitled');
    const total = conversations.length;
    const active = conversations.filter((conversation) => chatProjectFolderContext.runningIds.has(conversation.id)).length;
    const fallbackStats = `${total} 个任务 · ${active} 个已开启`;
    const description = clampProjectPreviewText(project.description)
        || pickerMessage(tp, 'chat.projectPreviewNoDescription', '暂无项目说明');
    const scope = getProjectScopePreview(project);

    preview.dataset.projectId = project.id || '';
    preview.classList.toggle('is-unassigned', isUnassigned);
    preview.querySelector('.project-folder-preview-title').textContent = title;
    preview.querySelector('.project-folder-preview-stats span').textContent = tpFmt(
        'chat.projectPreviewStats',
        fallbackStats,
        { total, active }
    );
    const descriptionRow = preview.querySelector('.project-folder-preview-description');
    descriptionRow.querySelector('span').textContent = description;
    descriptionRow.classList.toggle('is-empty', !String(project.description || '').trim());
    const scopeRow = preview.querySelector('.project-folder-preview-scope');
    scopeRow.hidden = isUnassigned || !scope;
    scopeRow.querySelector('span').textContent = scope
        ? tpFmt('chat.projectPreviewScope', `测试范围：${scope}`, { scope })
        : '';
    const editButton = preview.querySelector('.project-folder-preview-edit');
    editButton.hidden = isUnassigned;
    editButton.querySelector('span').textContent = tpFmt(
        'chat.projectPreviewEdit',
        '编辑项目'
    );
    preview.hidden = false;
    row.querySelector('.project-folder-item')?.setAttribute('aria-controls', preview.id);
    positionProjectFolderPreview(preview, row);
    if (typeof applyRBACToUI === 'function') applyRBACToUI(preview);
}

function scheduleShowProjectFolderPreview(project, row, conversations, immediate = false) {
    clearTimeout(projectFolderPreviewOpenTimer);
    clearTimeout(projectFolderPreviewCloseTimer);
    projectFolderPreviewOpenTimer = setTimeout(
        () => showProjectFolderPreview(project, row, conversations),
        immediate ? 0 : PROJECT_FOLDER_PREVIEW_OPEN_DELAY_MS
    );
}

function hideProjectFolderPreview(immediate = false) {
    clearTimeout(projectFolderPreviewOpenTimer);
    clearTimeout(projectFolderPreviewCloseTimer);
    const close = () => {
        const preview = document.getElementById('project-folder-preview');
        if (preview) preview.hidden = true;
        projectFolderPreviewAnchor?.querySelector('.project-folder-item')?.removeAttribute('aria-controls');
        projectFolderPreviewAnchor = null;
    };
    if (immediate) close();
    else projectFolderPreviewCloseTimer = setTimeout(close, PROJECT_FOLDER_PREVIEW_CLOSE_DELAY_MS);
}

function scheduleHideProjectFolderPreview() {
    hideProjectFolderPreview(false);
}

function formatProjectConversationPreviewAge(value) {
    const timestamp = Date.parse(value || '');
    if (!Number.isFinite(timestamp)) return '';
    const date = new Date(timestamp);
    const pad = (part) => String(part).padStart(2, '0');
    const parts = {
        year: String(date.getFullYear()),
        month: pad(date.getMonth() + 1),
        day: pad(date.getDate()),
        hour: pad(date.getHours()),
        minute: pad(date.getMinutes()),
    };
    return tpFmt(
        'chat.conversationPreviewDateTime',
        `${parts.year}年${parts.month}月${parts.day}日 ${parts.hour}:${parts.minute}`,
        parts
    );
}

function getProjectConversationModeLabel(conversation) {
    const mode = String(conversation?.agentMode || conversation?.agent_mode || '').trim().toLowerCase();
    const labels = {
        eino_single: ['chat.agentModeEinoSingle', 'Eino 单代理（ADK）'],
        deep: ['chat.agentModeDeep', 'Deep（DeepAgent）'],
        plan_execute: ['chat.agentModePlanExecuteLabel', 'Plan-Execute'],
        supervisor: ['chat.agentModeSupervisorLabel', 'Supervisor（专家路由）'],
    };
    if (labels[mode]) return tpFmt(labels[mode][0], labels[mode][1]);
    return clampProjectPreviewText(
        conversation?.roleName || conversation?.role_name || pickerMessage(tp, 'chat.conversationPreviewDefaultMode', '默认'),
        80
    );
}

function getProjectConversationModeIconClass(conversation) {
    const mode = String(conversation?.agentMode || conversation?.agent_mode || '').trim().toLowerCase();
    if (mode === 'eino_single') return 'eino';
    if (mode === 'deep') return 'deep';
    if (mode === 'plan_execute') return 'plan';
    if (mode === 'supervisor') return 'supervisor';
    return 'default';
}

function ensureProjectConversationPreview() {
    let preview = document.getElementById('project-conversation-preview');
    if (preview) return preview;
    preview = document.createElement('div');
    preview.id = 'project-conversation-preview';
    preview.className = 'project-conversation-preview';
    preview.hidden = true;
    preview.setAttribute('role', 'tooltip');
    preview.innerHTML = `
        <div class="project-conversation-preview-header">
            <span class="project-conversation-preview-title"></span>
            <span class="project-conversation-preview-age"></span>
        </div>
        <div class="project-conversation-preview-meta">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M3 6.5h6l2 2h10v9.5a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"/></svg>
            <span class="project-conversation-preview-project"></span>
        </div>
        <div class="project-conversation-preview-meta">
            <span class="project-conversation-preview-mode-icon agent-mode-logo agent-mode-logo--default" aria-hidden="true"><svg class="agent-mode-logo__svg" viewBox="0 0 24 24" fill="none" aria-hidden="true"><rect x="3" y="11" width="18" height="10" rx="2"/><circle cx="12" cy="5" r="2"/><path d="M12 7v4"/><path d="M8 16h.01"/><path d="M16 16h.01"/></svg></span>
            <span class="project-conversation-preview-mode"></span>
            <span class="project-conversation-preview-separator" aria-hidden="true">·</span>
            <span class="project-conversation-preview-status"></span>
        </div>
    `;
    document.body.appendChild(preview);
    window.addEventListener('resize', () => hideProjectConversationPreview(true));
    window.addEventListener('scroll', () => hideProjectConversationPreview(true), true);
    return preview;
}

function positionProjectConversationPreview(preview, row) {
    if (!preview || !row?.isConnected) return;
    const anchorRect = row.getBoundingClientRect();
    const previewRect = preview.getBoundingClientRect();
    const gap = 10;
    const edge = 12;
    let left = anchorRect.right + gap;
    if (left + previewRect.width > window.innerWidth - edge) {
        left = Math.max(edge, anchorRect.left - previewRect.width - gap);
    }
    const top = Math.min(
        Math.max(edge, anchorRect.top - 8),
        Math.max(edge, window.innerHeight - previewRect.height - edge)
    );
    preview.style.left = `${Math.round(left)}px`;
    preview.style.top = `${Math.round(top)}px`;
}

function showProjectConversationPreview(conversation, project, row) {
    if (!conversation || !row?.isConnected || window.matchMedia('(max-width: 900px), (hover: none)').matches) return;
    hideProjectFolderPreview(true);
    const preview = ensureProjectConversationPreview();
    clearTimeout(projectConversationPreviewCloseTimer);
    if (projectConversationPreviewAnchor && projectConversationPreviewAnchor !== row) {
        projectConversationPreviewAnchor.querySelector('.project-conversation-item')?.removeAttribute('aria-describedby');
    }
    projectConversationPreviewAnchor = row;
    const isRunning = chatProjectFolderContext.runningIds.has(conversation.id);
    const isWaitingApproval = chatProjectFolderContext.pendingApprovalByConversation.has(conversation.id);
    const completed = chatProjectFolderContext.completedByConversation.get(conversation.id);
    const isUnread = !isRunning && isProjectConversationUnread(conversation.id);
    const status = isWaitingApproval
        ? pickerMessage(tp, 'hitl.waitingApprovalShort', '等待批准')
        : (isRunning
        ? pickerMessage(tp, 'tasks.statusRunning', '执行中')
        : (isUnread
            ? pickerMessage(tp, 'chat.conversationPreviewUnread', '有未读更新')
            : (completed
                ? pickerMessage(tp, 'chat.conversationPreviewViewed', '已查看')
                : pickerMessage(tp, 'chat.conversationPreviewConversation', '对话'))));
    const statusEl = preview.querySelector('.project-conversation-preview-status');

    preview.querySelector('.project-conversation-preview-title').textContent = conversation.title
        || pickerMessage(tp, 'projects.untitledConversation', '未命名对话');
    const ageEl = preview.querySelector('.project-conversation-preview-age');
    ageEl.textContent = formatProjectConversationPreviewAge(
        conversation.updatedAt || conversation.updated_at || conversation.createdAt || conversation.created_at
    );
    ageEl.hidden = !ageEl.textContent;
    preview.querySelector('.project-conversation-preview-project').textContent = project?.name
        || pickerMessage(tp, 'chat.conversationPreviewNoProject', '未绑定项目');
    const modeIcon = preview.querySelector('.project-conversation-preview-mode-icon');
    if (modeIcon) {
        modeIcon.className = 'project-conversation-preview-mode-icon agent-mode-logo agent-mode-logo--' + getProjectConversationModeIconClass(conversation);
    }
    preview.querySelector('.project-conversation-preview-mode').textContent = getProjectConversationModeLabel(conversation);
    statusEl.textContent = status;
    statusEl.className = 'project-conversation-preview-status'
        + (isWaitingApproval ? ' is-approval' : (isRunning ? ' is-running' : (isUnread ? ' is-unread' : '')));
    preview.hidden = false;
    row.querySelector('.project-conversation-item')?.setAttribute('aria-describedby', preview.id);
    positionProjectConversationPreview(preview, row);
}

function scheduleShowProjectConversationPreview(conversation, project, row, immediate = false) {
    if (Date.now() < projectConversationPreviewSuppressedUntil) return;
    clearTimeout(projectConversationPreviewOpenTimer);
    clearTimeout(projectConversationPreviewCloseTimer);
    projectConversationPreviewOpenTimer = setTimeout(
        () => showProjectConversationPreview(conversation, project, row),
        immediate ? 0 : PROJECT_FOLDER_PREVIEW_OPEN_DELAY_MS
    );
}

function hideProjectConversationPreview(immediate = false) {
    clearTimeout(projectConversationPreviewOpenTimer);
    clearTimeout(projectConversationPreviewCloseTimer);
    const close = () => {
        const preview = document.getElementById('project-conversation-preview');
        if (preview) preview.hidden = true;
        projectConversationPreviewAnchor?.querySelector('.project-conversation-item')?.removeAttribute('aria-describedby');
        projectConversationPreviewAnchor = null;
    };
    if (immediate) close();
    else projectConversationPreviewCloseTimer = setTimeout(close, PROJECT_FOLDER_PREVIEW_CLOSE_DELAY_MS);
}

function appendChatProjectFolderItem(list, project, expandedIds, conversations) {
    const row = document.createElement('div');
    const isUnassigned = project._isUnassigned === true;
    const folderId = isUnassigned ? CHAT_UNASSIGNED_PROJECT_FOLDER_ID : project.id;
    const isExpanded = expandedIds.has(folderId);
    const statusKinds = getProjectFolderStatuses(conversations);
    row.className = 'project-folder-row'
        + (isExpanded ? ' is-expanded' : '')
        + (isUnassigned ? ' is-unassigned' : '');

    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'project-folder-item';
    button.dataset.projectId = project.id || '';
    button.dataset.folderId = folderId;
    button.setAttribute('aria-label', project.name || tp('common.untitled'));
    button.setAttribute('aria-expanded', isExpanded ? 'true' : 'false');

    const disclosure = document.createElement('span');
    disclosure.className = 'project-folder-disclosure';
    disclosure.setAttribute('aria-hidden', 'true');
    disclosure.innerHTML = projectFolderDisclosureMarkup(isExpanded);

    const icon = document.createElement('span');
    icon.className = 'project-folder-icon';
    icon.setAttribute('aria-hidden', 'true');
    icon.innerHTML = projectFolderIconMarkup(isExpanded);

    const title = document.createElement('span');
    title.className = 'project-folder-title';
    window.applyProjectNameDisplay(title, project.name, tp('common.untitled'));

    const label = document.createElement('span');
    label.className = 'project-folder-label';
    label.appendChild(title);
    if (!isUnassigned && project.pinned) {
        const pinIcon = document.createElement('span');
        pinIcon.className = 'project-folder-pinned';
        pinIcon.textContent = '📌';
        pinIcon.title = pickerMessage(tp, 'projects.pinned', '已置顶');
        pinIcon.setAttribute('aria-label', pinIcon.title);
        label.appendChild(pinIcon);
    }
    const folderApprovals = conversations
        .map((conversation) => chatProjectFolderContext.pendingApprovalByConversation.get(conversation.id))
        .filter(Boolean);
    const folderStatusGroup = appendProjectTaskStatuses(
        label,
        statusKinds,
        { approval: null },
        { approval: { aggregate: true, count: folderApprovals.length } }
    );
    if (folderStatusGroup) folderStatusGroup.classList.add('project-task-status-group--folder');

    button.appendChild(disclosure);
    button.appendChild(icon);
    button.appendChild(label);
    button.addEventListener('click', () => {
        if (isExpanded) {
            chatProjectFolderExpandedIds.delete(folderId);
        } else {
            chatProjectFolderExpandedIds.add(folderId);
        }
        renderChatProjectFolders(projectsCacheAll);
    });
    row.addEventListener('mouseenter', () => scheduleShowProjectFolderPreview(project, row, conversations));
    row.addEventListener('mouseleave', scheduleHideProjectFolderPreview);
    button.addEventListener('focus', () => scheduleShowProjectFolderPreview(project, row, conversations, true));
    row.addEventListener('focusout', (event) => {
        const preview = document.getElementById('project-folder-preview');
        if (row.contains(event.relatedTarget) || preview?.contains(event.relatedTarget)) return;
        scheduleHideProjectFolderPreview();
    });

    const actions = document.createElement('div');
    actions.className = 'project-folder-actions';

    if (!isUnassigned) {
        const menuButton = document.createElement('button');
        menuButton.type = 'button';
        menuButton.className = 'project-folder-action project-folder-menu';
        menuButton.setAttribute('aria-label', tp('projects.projectActions'));
        menuButton.title = menuButton.getAttribute('aria-label');
        menuButton.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="5" cy="12" r="1.6"/><circle cx="12" cy="12" r="1.6"/><circle cx="19" cy="12" r="1.6"/></svg>';
        menuButton.addEventListener('click', (event) => {
            showProjectListActionMenu(event, project.id, 'chat');
        });
        actions.appendChild(menuButton);
    }

    const newConversationButton = document.createElement('button');
    newConversationButton.type = 'button';
    newConversationButton.className = 'project-folder-action project-folder-new-conversation';
    newConversationButton.setAttribute(
        'aria-label',
        isUnassigned
            ? pickerMessage(tp, 'chat.newUnassignedConversation', '新建无项目对话')
            : pickerMessage(tp, 'chat.newConversationInProject', '在此项目中新建对话')
    );
    newConversationButton.title = newConversationButton.getAttribute('aria-label');
    newConversationButton.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>';
    newConversationButton.addEventListener('click', async (event) => {
        event.preventDefault();
        event.stopPropagation();
        chatProjectFolderExpandedIds.add(folderId);
        if (typeof window.startNewConversation === 'function') {
            await window.startNewConversation({ projectId: isUnassigned ? '' : project.id });
        }
        renderChatProjectFolders(projectsCacheAll);
    });

    actions.appendChild(newConversationButton);
    row.appendChild(button);
    row.appendChild(actions);
    list.appendChild(row);
}

function appendChatProjectConversationItem(list, conversation, project) {
    const row = document.createElement('div');
    const button = document.createElement('button');
    const isSelected = window.currentConversationId === conversation.id;
    const isRunning = chatProjectFolderContext.runningIds.has(conversation.id);
    const isWaitingApproval = chatProjectFolderContext.pendingApprovalByConversation.has(conversation.id);
    const completed = chatProjectFolderContext.completedByConversation.get(conversation.id);
    const isUnread = !isRunning && isProjectConversationUnread(conversation.id);
    row.className = 'project-conversation-row' + (isSelected ? ' is-selected' : '');
    button.type = 'button';
    button.className = 'project-conversation-item' + (isSelected ? ' is-selected' : '');
    button.dataset.conversationId = conversation.id;
    if (isSelected) button.setAttribute('aria-current', 'true');

    const title = document.createElement('span');
    title.className = 'project-conversation-title';
    title.textContent = conversation.title || pickerMessage(tp, 'projects.untitledConversation', '未命名对话');

    const label = document.createElement('span');
    label.className = 'project-conversation-label';
    label.appendChild(title);
    if (conversation.pinned) {
        const pinIcon = document.createElement('span');
        pinIcon.className = 'conversation-item-pinned project-conversation-pinned';
        pinIcon.textContent = '📌';
        pinIcon.title = pickerMessage(tp, 'projects.pinned', '已置顶');
        pinIcon.setAttribute('aria-label', pinIcon.title);
        label.appendChild(pinIcon);
    }
    const statusKinds = [];
    if (isWaitingApproval) statusKinds.push('approval');
    if (isRunning) statusKinds.push('running');
    else if (isUnread) statusKinds.push('unread');
    if (statusKinds.length) appendProjectTaskStatuses(label, statusKinds, {
        approval: chatProjectFolderContext.pendingApprovalByConversation.get(conversation.id)
    });
    button.appendChild(label);

    button.addEventListener('click', async (event) => {
        const targetConversationId = String(event.currentTarget && event.currentTarget.dataset.conversationId || '').trim();
        if (!targetConversationId) return;
        projectConversationPreviewSuppressedUntil = Date.now() + 700;
        hideProjectConversationPreview(true);
        selectChatProjectConversationItem(targetConversationId);
        if (typeof window.loadConversation === 'function') {
            await window.loadConversation(targetConversationId);
        }
        if (window.currentConversationId === targetConversationId && completed) {
            markProjectConversationViewed(targetConversationId, completed.completedAt);
            renderChatProjectFolders(projectsCacheAll);
        }
    });
    row.addEventListener('mouseenter', () => scheduleShowProjectConversationPreview(conversation, project, row));
    row.addEventListener('mouseleave', () => hideProjectConversationPreview(false));
    button.addEventListener('focus', () => scheduleShowProjectConversationPreview(conversation, project, row, true));
    row.addEventListener('focusout', (event) => {
        if (row.contains(event.relatedTarget)) return;
        hideProjectConversationPreview(false);
    });
    const menuButton = document.createElement('button');
    menuButton.type = 'button';
    menuButton.className = 'project-conversation-menu';
    menuButton.setAttribute(
        'aria-label',
        pickerMessage(tp, 'chat.conversationActions', '对话操作')
    );
    menuButton.title = menuButton.getAttribute('aria-label');
    menuButton.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="5" cy="12" r="1.6"/><circle cx="12" cy="12" r="1.6"/><circle cx="19" cy="12" r="1.6"/></svg>';
    menuButton.addEventListener('click', (event) => {
        if (typeof window.openConversationContextMenuForId === 'function') {
            window.openConversationContextMenuForId(event, conversation.id, conversation.title || '');
        }
    });

    row.appendChild(button);
    row.appendChild(menuButton);
    list.appendChild(row);
}

function selectChatProjectConversationItem(conversationId) {
    const id = String(conversationId || '').trim();
    document.querySelectorAll('.project-conversation-row').forEach((row) => {
        const button = row.querySelector('.project-conversation-item');
        const selected = !!id && button?.dataset.conversationId === id;
        row.classList.toggle('is-selected', selected);
        if (!button) return;
        button.classList.toggle('is-selected', selected);
        if (selected) button.setAttribute('aria-current', 'true');
        else button.removeAttribute('aria-current');
    });
}

window.selectChatProjectConversationItem = selectChatProjectConversationItem;

async function loadChatProjectFolderContext() {
    const loadSeq = ++chatProjectFolderContextLoadSeq;
    const conversationsParams = new URLSearchParams({ limit: '1000', offset: '0', sort_by: 'updated_at' });
    const [conversationResponse, activeResponse, completedResponse, pendingResponse] = await Promise.all([
        apiFetch(`/api/conversations?${conversationsParams}`),
        apiFetch('/api/agent-loop/tasks'),
        apiFetch('/api/agent-loop/tasks/completed'),
        apiFetch('/api/hitl/pending?page=1&pageSize=200'),
    ]);
    if (!conversationResponse.ok) throw new Error(tp('projects.loadProjectsFailed'));
    const conversationData = await conversationResponse.json();
    const activeData = activeResponse.ok ? await activeResponse.json() : { tasks: [] };
    const completedData = completedResponse.ok ? await completedResponse.json() : { tasks: [] };
    const pendingData = pendingResponse.ok ? await pendingResponse.json() : { items: [] };
    if (loadSeq !== chatProjectFolderContextLoadSeq) return false;
    const conversations = Array.isArray(conversationData)
        ? conversationData
        : (conversationData.conversations || conversationData.items || []);
    chatProjectFolderContext.conversations = Array.isArray(conversations) ? conversations : [];
    chatProjectFolderContext.runningIds = new Set(
        (activeData.tasks || [])
            .filter((task) => task && !['completed', 'failed', 'timeout', 'cancelled'].includes(String(task.status || '').toLowerCase()))
            .map((task) => task.conversationId)
            .filter(Boolean)
    );
    chatProjectFolderContext.completedByConversation = new Map();
    (completedData.tasks || []).forEach((task) => {
        if (task?.conversationId && !chatProjectFolderContext.completedByConversation.has(task.conversationId)) {
            chatProjectFolderContext.completedByConversation.set(task.conversationId, task);
        }
    });
    chatProjectFolderContext.pendingApprovalByConversation = new Map();
    (pendingData.items || []).filter(isHumanProjectPendingApproval).forEach((item) => {
        const conversationId = String(item?.conversationId || '').trim();
        // pending 审批必须属于当前进程仍在运行的任务；服务重启/取消后的旧记录
        // 即使在并发窗口内被读到，也不能重新显示倒计时徽标。
        if (conversationId && chatProjectFolderContext.runningIds.has(conversationId) &&
            !chatProjectFolderContext.pendingApprovalByConversation.has(conversationId)) {
            chatProjectFolderContext.pendingApprovalByConversation.set(conversationId, item);
        }
    });
    chatProjectFolderContext.ready = true;
    return true;
}

function isHumanProjectPendingApproval(item) {
    if (!item) return false;
    const reviewer = String(item.reviewer || item.decidedBy || item.decided_by || '').trim().toLowerCase();
    const status = String(item.status || '').trim().toLowerCase();
    return reviewer !== 'audit_agent' && reviewer !== 'agent' && reviewer !== 'ai' && status !== 'audit_running';
}

function getProjectConversationSortTime(conversation) {
    const value = conversation?.updatedAt || conversation?.updated_at
        || conversation?.createdAt || conversation?.created_at || '';
    const timestamp = Date.parse(value);
    return Number.isFinite(timestamp) ? timestamp : 0;
}

function sortProjectFolderConversations(conversations) {
    return [...conversations].sort((a, b) => {
        const pinnedDelta = Number(!!b?.pinned) - Number(!!a?.pinned);
        if (pinnedDelta) return pinnedDelta;
        return getProjectConversationSortTime(b) - getProjectConversationSortTime(a);
    });
}

function updateChatProjectConversationPinnedState(conversationId, pinned) {
    const id = String(conversationId || '').trim();
    const conversation = chatProjectFolderContext.conversations.find(
        (item) => String(item?.id || '').trim() === id
    );
    if (!conversation) return false;
    conversation.pinned = !!pinned;
    if (isProjectsCacheReady() && chatProjectFolderContext.ready) {
        renderChatProjectFolders(projectsCacheAll);
    }
    return true;
}

function removeChatProjectConversation(conversationId) {
    const id = String(conversationId || '').trim();
    if (!id) return false;
    const previousLength = chatProjectFolderContext.conversations.length;
    chatProjectFolderContext.conversations = chatProjectFolderContext.conversations.filter(
        (item) => String(item?.id || '').trim() !== id
    );
    chatProjectFolderContext.runningIds.delete(id);
    chatProjectFolderContext.completedByConversation.delete(id);
    chatProjectFolderContext.pendingApprovalByConversation.delete(id);
    if (projectConversationPreviewAnchor?.querySelector('.project-conversation-item')?.dataset.conversationId === id) {
        hideProjectConversationPreview(true);
    }
    if (isProjectsCacheReady() && chatProjectFolderContext.ready) {
        renderChatProjectFolders(projectsCacheAll);
    }
    return chatProjectFolderContext.conversations.length !== previousLength;
}

function refreshChatProjectFoldersAfterAction() {
    Promise.resolve().then(() => refreshChatProjectFolders()).catch((error) => {
        console.warn('刷新项目对话列表失败:', error);
    });
}

function resolveChatProjectFolderSelection() {
    const conversationId = String(window.currentConversationId || '').trim();
    if (!conversationId) return resolveChatProjectSelection();
    if (!chatProjectFolderContext.ready) return null;

    const conversation = chatProjectFolderContext.conversations.find(
        (item) => String(item?.id || '').trim() === conversationId
    );
    if (!conversation) return null;
    return String(conversation.projectId || conversation.project_id || '').trim();
}

function appendChatProjectFoldersLoadMore(list, remainingCount) {
    if (!list || remainingCount <= 0) return;
    const button = document.createElement('button');
    const label = pickerMessage(tp, 'common.loadMore', '加载更多');
    button.type = 'button';
    button.className = 'project-folders-load-more';
    button.setAttribute('aria-label', tpFmt(
        'chat.projectFoldersLoadMoreRemaining',
        `${label}，剩余 ${remainingCount} 个项目`,
        { count: remainingCount }
    ));
    button.innerHTML = `<span>${escapeHtml(label)}</span><span class="project-folders-load-more-count">${remainingCount}</span>`;
    button.addEventListener('click', loadMoreChatProjectFolders);
    list.appendChild(button);
}

function loadMoreChatProjectFolders() {
    chatProjectFolderVisibleCount += CHAT_PROJECT_FOLDER_PAGE_SIZE;
    if (isProjectsCacheReady() && chatProjectFolderContext.ready) {
        renderChatProjectFolders(projectsCacheAll);
    } else {
        refreshChatProjectFolders();
    }
}

function renderChatProjectFolders(projects) {
    const list = document.getElementById('project-folders-list');
    if (!list) return;
    hideProjectFolderPreview(true);
    hideProjectConversationPreview(true);
    const selectedId = resolveChatProjectFolderSelection();
    if (selectedId !== null && chatProjectFolderLastSelectionId !== selectedId) {
        chatProjectFolderExpandedIds.add(selectedId || CHAT_UNASSIGNED_PROJECT_FOLDER_ID);
        chatProjectFolderLastSelectionId = selectedId;
    }
    const filtered = filterActiveProjectsLocal(projects, chatProjectFolderSearchQuery);
    const unassignedProject = {
        id: '',
        name: tp('projects.noProject'),
        description: tp('projects.noProjectDescription'),
        _isUnassigned: true,
    };
    const includeUnassigned = matchProjectSearchQuery(unassignedProject, chatProjectFolderSearchQuery);
    const pinnedProjects = filtered.filter((project) => !!project.pinned);
    const regularProjects = filtered.filter((project) => !project.pinned);
    const folders = includeUnassigned
        ? [...pinnedProjects, unassignedProject, ...regularProjects]
        : filtered;
    const queryKey = chatProjectFolderSearchQuery.toLocaleLowerCase();
    if (queryKey !== chatProjectFolderLastQuery) {
        chatProjectFolderLastQuery = queryKey;
        chatProjectFolderVisibleCount = CHAT_PROJECT_FOLDER_PAGE_SIZE;
    }
    const selectedFolderId = selectedId === null
        ? null
        : (selectedId || CHAT_UNASSIGNED_PROJECT_FOLDER_ID);
    if (selectedFolderId !== null) {
        const selectedIndex = folders.findIndex((project) => (
            project._isUnassigned ? CHAT_UNASSIGNED_PROJECT_FOLDER_ID : project.id
        ) === selectedFolderId);
        if (selectedIndex >= chatProjectFolderVisibleCount) {
            chatProjectFolderVisibleCount = selectedIndex + 1;
        }
    }
    const visibleFolders = folders.slice(0, chatProjectFolderVisibleCount);
    list.innerHTML = '';
    if (!folders.length) {
        const empty = document.createElement('div');
        empty.className = 'project-folders-empty';
        empty.textContent = chatProjectFolderSearchQuery
            ? pickerMessage(tp, 'chat.filterProjectSearchEmpty', '没有匹配的项目')
            : pickerMessage(tp, 'projects.noProjects', '暂无项目');
        list.appendChild(empty);
        return;
    }
    visibleFolders.forEach((project) => {
        const folderId = project._isUnassigned ? CHAT_UNASSIGNED_PROJECT_FOLDER_ID : project.id;
        const conversations = sortProjectFolderConversations(
            chatProjectFolderContext.conversations
                .filter((conversation) => (conversation.projectId || conversation.project_id || '') === project.id)
        );
        appendChatProjectFolderItem(list, project, chatProjectFolderExpandedIds, conversations);
        if (chatProjectFolderExpandedIds.has(folderId)) {
            conversations.forEach((conversation) => appendChatProjectConversationItem(list, conversation, project));
        }
    });
    appendChatProjectFoldersLoadMore(list, folders.length - visibleFolders.length);
}

async function refreshChatProjectFolders() {
    const list = document.getElementById('project-folders-list');
    if (!list) return;
    const seq = ++chatProjectFolderRenderSeq;
    if (!isProjectsCacheReady()) {
        list.innerHTML = '';
        appendChatProjectPanelMessage(list, 'project-folders-empty', pickerMessage(tp, 'common.loading', '加载中…'));
    }
    try {
        const [projects] = await Promise.all([
            ensureProjectsLoaded(),
            loadChatProjectFolderContext(),
        ]);
        if (seq !== chatProjectFolderRenderSeq) return;
        renderChatProjectFolders(projects);
    } catch (e) {
        if (seq !== chatProjectFolderRenderSeq) return;
        list.innerHTML = '';
        appendChatProjectPanelMessage(
            list,
            'project-folders-empty',
            pickerMessage(tp, 'projects.loadProjectsFailed', '加载项目失败')
        );
    }
}

function handleProjectFolderSearch(value) {
    chatProjectFolderSearchQuery = String(value || '').trim();
    const clearButton = document.getElementById('conversation-search-clear');
    if (clearButton) clearButton.style.display = chatProjectFolderSearchQuery ? 'flex' : 'none';
    if (isProjectsCacheReady() && chatProjectFolderContext.ready) {
        renderChatProjectFolders(projectsCacheAll);
    } else {
        refreshChatProjectFolders();
    }
}

function clearProjectFolderSearch() {
    const input = document.getElementById('conversation-search-input');
    if (input) input.value = '';
    handleProjectFolderSearch('');
    input?.focus();
}

function updateProjectFolderTaskStatuses(tasks) {
    const previous = chatProjectFolderContext.runningIds;
    const next = new Set(
        (Array.isArray(tasks) ? tasks : [])
            .filter((task) => task && !['completed', 'failed', 'timeout', 'cancelled'].includes(String(task.status || '').toLowerCase()))
            .map((task) => task.conversationId)
            .filter(Boolean)
    );
    const taskFinished = [...previous].some((conversationId) => !next.has(conversationId));
    let approvalChanged = false;
    chatProjectFolderContext.pendingApprovalByConversation.forEach((_details, conversationId) => {
        if (next.has(conversationId)) return;
        chatProjectFolderContext.pendingApprovalByConversation.delete(conversationId);
        approvalChanged = true;
    });
    const changed = previous.size !== next.size || [...previous].some((conversationId) => !next.has(conversationId));
    if (!changed && !approvalChanged) return;
    chatProjectFolderContext.runningIds = next;
    if (isProjectsCacheReady() && chatProjectFolderContext.ready) {
        renderChatProjectFolders(projectsCacheAll);
    }
    if (taskFinished) refreshChatProjectFolders();
}

function setProjectConversationApprovalStatus(conversationId, pending, details) {
    const id = String(conversationId || '').trim();
    if (!id) return;
    if (pending && isHumanProjectPendingApproval(details || {})) chatProjectFolderContext.pendingApprovalByConversation.set(id, details || { conversationId: id });
    else chatProjectFolderContext.pendingApprovalByConversation.delete(id);
    if (isProjectsCacheReady() && chatProjectFolderContext.ready) {
        renderChatProjectFolders(projectsCacheAll);
    }
}

function syncProjectConversationApprovalStatuses(items) {
    const next = new Map();
    (Array.isArray(items) ? items : []).forEach((details) => {
        if (!isHumanProjectPendingApproval(details)) return;
        const id = String(details && details.conversationId || '').trim();
        if (id && chatProjectFolderContext.runningIds.has(id) && !next.has(id)) {
            next.set(id, details);
        }
    });
    let changed = next.size !== chatProjectFolderContext.pendingApprovalByConversation.size;
    if (!changed) {
        next.forEach((details, id) => {
            const previous = chatProjectFolderContext.pendingApprovalByConversation.get(id);
            const previousInterruptId = String(previous && (previous.interruptId || previous.id) || '');
            const nextInterruptId = String(details && (details.interruptId || details.id) || '');
            if (!previous || previousInterruptId !== nextInterruptId) changed = true;
        });
    }
    if (!changed) return;
    chatProjectFolderContext.pendingApprovalByConversation = next;
    if (isProjectsCacheReady() && chatProjectFolderContext.ready) {
        renderChatProjectFolders(projectsCacheAll);
    }
}

window.setProjectConversationApprovalStatus = setProjectConversationApprovalStatus;
window.syncProjectConversationApprovalStatuses = syncProjectConversationApprovalStatuses;

if (!window._projectConversationActionEventsBound) {
        document.addEventListener('conversation-pinned-changed', (event) => {
        const details = event?.detail || {};
        updateChatProjectConversationPinnedState(details.conversationId, details.pinned);
        refreshChatProjectFoldersAfterAction();
    });
    document.addEventListener('conversation-deleted', (event) => {
        const conversationId = event?.detail?.conversationId;
        removeChatProjectConversation(conversationId);
        refreshChatProjectFoldersAfterAction();
    });
}

if (!window._projectApprovalStatusEventsBound) {
        window.addEventListener('hitl-interrupt', (event) => {
        const details = event && event.detail ? event.detail : {};
        setProjectConversationApprovalStatus(details.conversationId || window.currentConversationId || '', true, details);
    });
    window.addEventListener('hitl-resolved', (event) => {
        const details = event && event.detail ? event.detail : {};
        setProjectConversationApprovalStatus(details.conversationId || window.currentConversationId || '', false);
    });
}

function appendChatProjectPanelItem(list, project, selectedId, onSelect, tFn) {
    const t = tFn || tp;
    const isNone = !project.id;
    const isSelected = isNone ? !selectedId : selectedId === project.id;
    const fullDesc = isNone
        ? (project.description || '')
        : (project.description || '').trim() || '';
    const desc = isNone
        ? (project.description || '')
        : fullDesc.slice(0, 80);
    const fullName = project.name || t('common.untitled');
    const displayName = window.formatProjectNameForDisplay(fullName);
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'role-selection-item-main' + (isSelected ? ' selected' : '');
    btn.setAttribute('role', 'option');
    btn.setAttribute('aria-label', fullName);
    btn.setAttribute('data-selection-detail', fullDesc);
    btn.onclick = () => onSelect(project.id || '');
    btn.innerHTML = `
        <div class="role-selection-item-icon-main">${isNone ? '—' : '📁'}</div>
        <div class="role-selection-item-content-main">
            <div class="role-selection-item-name-main" title="${escapeAttr(fullName)}">${escapeHtml(displayName)}</div>
            <div class="role-selection-item-description-main">${escapeHtml(desc)}</div>
        </div>
        ${isSelected ? '<div class="role-selection-checkmark-main">✓</div>' : ''}
    `;
    list.appendChild(btn);
}

function appendChatProjectPanelMessage(list, className, text) {
    const el = document.createElement('div');
    el.className = className;
    el.textContent = text;
    list.appendChild(el);
    return el;
}

function pickerMessage(t, key, fallback) {
    const value = t(key);
    if (!value || value === key) return fallback;
    return value;
}

async function renderProjectPickerPanel(panelKey, config) {
    const state = projectPickerPanelState[panelKey];
    const list = document.getElementById(config.listId);
    if (!list || !state) return;
    const query = (document.getElementById(config.searchInputId)?.value || '').trim();
    const seq = ++state.seq;
    const selectedId = config.getSelectedId();
    const t = config.t || tp;

    const renderPinned = () => {
        appendChatProjectPanelItem(
            list,
            {
                id: '',
                name: t('projects.noProject'),
                description: t('projects.noProjectDescription'),
            },
            selectedId,
            config.onSelect,
            t
        );
    };

    const needsFetch = !isProjectsCacheReady();
    let loadingEl = null;
    if (needsFetch) {
        list.innerHTML = '';
        renderPinned();
        loadingEl = appendChatProjectPanelMessage(
            list,
            'chat-project-panel-loading',
            pickerMessage(t, 'common.loading', '加载中…')
        );
    }

    try {
        const all = await ensureProjectsLoaded();
        if (seq !== state.seq) return;

        list.innerHTML = '';
        renderPinned();
        const projects = filterActiveProjectsLocal(all, query);
        projects.forEach((p) => {
            appendChatProjectPanelItem(list, p, selectedId, config.onSelect, t);
        });

        if (query && projects.length === 0) {
            appendChatProjectPanelMessage(
                list,
                'chat-project-panel-empty',
                pickerMessage(t, 'chat.filterProjectSearchEmpty', '没有匹配的项目')
            );
        }
    } catch (e) {
        if (seq !== state.seq) return;
        list.innerHTML = '';
        renderPinned();
        appendChatProjectPanelMessage(
            list,
            'chat-project-panel-empty',
            pickerMessage(t, 'chat.filterProjectSearchFailed', '加载项目失败，请重试')
        );
    } finally {
        if (loadingEl && loadingEl.parentNode) loadingEl.remove();
    }
}

function initProjectPickerPanelSearch(panelKey, searchInputId, onSearch) {
    const input = document.getElementById(searchInputId);
    if (!input || input.dataset.pickerBound === panelKey) return;
    input.dataset.pickerBound = panelKey;
    input.addEventListener('input', onSearch);
    input.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
            if (panelKey === 'chat' && typeof closeChatProjectPanel === 'function') {
                closeChatProjectPanel();
            } else if (panelKey === 'webshell' && typeof wsCloseProjectPanel === 'function') {
                wsCloseProjectPanel();
            }
        }
    });
}

function clearProjectPickerPanelSearch(panelKey, searchInputId) {
    const state = projectPickerPanelState[panelKey];
    if (!state) return;
    state.seq += 1;
    if (state.timer) {
        clearTimeout(state.timer);
        state.timer = null;
    }
    const input = document.getElementById(searchInputId);
    if (input) input.value = '';
}

function scheduleProjectPickerPanelSearch(panelKey, loadFn) {
    const state = projectPickerPanelState[panelKey];
    if (!state) return;
    if (state.timer) clearTimeout(state.timer);
    state.timer = setTimeout(() => {
        state.timer = null;
        loadFn();
    }, PROJECT_PICKER_DEBOUNCE_MS);
}

async function loadChatProjectPanelList() {
    await renderProjectPickerPanel('chat', {
        listId: 'chat-project-list',
        searchInputId: 'chat-project-search',
        getSelectedId: resolveChatProjectSelection,
        onSelect: (projectId) => selectChatProject(projectId),
    });
}

async function ensureChatProjectButtonLabel() {
    const id = (resolveChatProjectSelection() || '').trim();
    if (id && !projectNameById[id]) {
        await fetchProjectSummary(id);
    }
    updateChatProjectButtonLabel();
}

function updateChatProjectButtonLabel() {
    const textEl = document.getElementById('chat-project-text');
    if (!textEl) return;
    const id = resolveChatProjectSelection();
    window.applyProjectNameDisplay(
        textEl,
        id && projectNameById[id] ? projectNameById[id] : tp('projects.noProject')
    );
    if (typeof window.refreshChatWelcomeEmptyState === 'function') {
        window.refreshChatWelcomeEmptyState();
    }
}

async function renderChatProjectPanel() {
    initProjectPickerPanelSearch('chat', 'chat-project-search', () => {
        scheduleProjectPickerPanelSearch('chat', () => loadChatProjectPanelList());
    });
    clearProjectPickerPanelSearch('chat', 'chat-project-search');
    await loadChatProjectPanelList();
    const panel = document.getElementById('chat-project-panel');
    if (panel && typeof applyRBACToUI === 'function') applyRBACToUI(panel);
    requestAnimationFrame(() => document.getElementById('chat-project-search')?.focus());
}

function closeChatProjectPanel() {
    const panel = document.getElementById('chat-project-panel');
    const btn = document.getElementById('chat-project-btn');
    if (panel) panel.style.display = 'none';
    if (btn) {
        btn.classList.remove('active');
        btn.setAttribute('aria-expanded', 'false');
    }
    clearProjectPickerPanelSearch('chat', 'chat-project-search');
}

async function toggleChatProjectPanel() {
    const panel = document.getElementById('chat-project-panel');
    const btn = document.getElementById('chat-project-btn');
    if (!panel) return;
    const isHidden = panel.style.display === 'none' || !panel.style.display;
    if (!isHidden) {
        closeChatProjectPanel();
        return;
    }
    if (typeof closeRoleSelectionPanel === 'function') closeRoleSelectionPanel();
    if (typeof closeAgentModePanel === 'function') closeAgentModePanel();
    if (typeof closeChatReasoningPanel === 'function') closeChatReasoningPanel();
    panel.style.display = 'flex';
    if (btn) {
        btn.classList.add('active');
        btn.setAttribute('aria-expanded', 'true');
    }
    await renderChatProjectPanel();
}

async function selectChatProject(projectId) {
    closeChatProjectPanel();
    await applyChatProjectSelection(projectId || '');
}

async function applyChatProjectSelection(projectId) {
    const prev = getChatProjectSelection();
    if (projectId === prev) {
        updateChatProjectButtonLabel();
        await refreshChatProjectFolders();
        return;
    }
    if (window.currentConversationId) {
        try {
            const res = await apiFetch(`/api/conversations/${encodeURIComponent(window.currentConversationId)}/project`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ projectId }),
            });
            if (!res.ok) {
                const err = await res.json().catch(() => ({}));
                throw new Error(err.error || res.statusText);
            }
            window._loadedConversationProjectId = projectId;
            if (typeof showNotification === 'function') {
                showNotification(projectId ? tp('projects.projectBound') : tp('projects.projectUnbound'), 'success');
            }
        } catch (e) {
            console.error(e);
            alert(tp('projects.updateProjectBindingFailed') + ': ' + (e.message || e));
            updateChatProjectButtonLabel();
            return;
        }
    } else {
        setActiveProjectId(projectId);
    }
    updateChatProjectButtonLabel();
    await refreshChatProjectFolders();
    if (typeof window.onConversationProjectBindingChanged === 'function') {
        window.onConversationProjectBindingChanged(projectId);
    }
}

/** 对话页项目选择器：同步按钮文案；若浮层已打开则刷新列表 */
async function refreshChatProjectSelector(options = {}) {
    if (!document.getElementById('chat-project-btn')) return;
    try {
        await normalizeStaleChatProjectSelection();
        await ensureChatProjectButtonLabel();
    } catch (e) {
        console.warn(e);
    }
    if (options.renderFolders !== false) {
        const reloadFolders = options.reloadFolders !== false || !isProjectsCacheReady() || !chatProjectFolderContext.ready;
        if (reloadFolders) await refreshChatProjectFolders();
        else renderChatProjectFolders(projectsCacheAll);
    }
    const panel = document.getElementById('chat-project-panel');
    if (panel && panel.style.display === 'flex') {
        await loadChatProjectPanelList();
    }
}

async function onChatProjectChange() {
    /* 兼容旧调用；新 UI 使用 selectChatProject */
    await applyChatProjectSelection(getChatProjectSelection());
}

function initChatProjectSelector() {
    if (window._chatProjectSelectorInited) return;
        if (!window._projectsLanguageListenerBound) {
                document.addEventListener('languagechange', () => {
            renderProjectsSidebar();
            renderProjectsPagination();
            syncAllProjectsFilterSelects();
            updateChatProjectButtonLabel();
            refreshChatProjectFolders();
            const panel = document.getElementById('chat-project-panel');
            if (panel && panel.style.display === 'flex') loadChatProjectPanelList();
            if (currentProjectId) {
                refreshProjectDetailMetaI18n();
                const source = projectsCacheAll.length ? projectsCacheAll : projectsCache;
                const p = source.find((x) => x.id === currentProjectId);
                if (p) updateProjectStatusPill(p.status || 'active');
                refreshProjectHeaderStats().catch(() => {});
                switchProjectTab(currentProjectTab || 'conversations');
            }
        });
    }
    refreshChatProjectSelector().catch(() => {});
    document.addEventListener('click', (e) => {
        const panel = document.getElementById('chat-project-panel');
        const wrapper = document.querySelector('.project-selector-wrapper');
        if (!panel || panel.style.display === 'none' || !panel.style.display) return;
        if (!wrapper?.contains(e.target)) {
            closeChatProjectPanel();
        }
    });
}


if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => {
        initChatProjectSelector();
        initProjectConversationReadTracking();
        initProjectListActionMenu();
        initProjectGraphFooterWheelScroll();
        refreshProjectsFilterSelects();
    });
} else {
    initChatProjectSelector();
    initProjectConversationReadTracking();
    initProjectListActionMenu();
    initProjectGraphFooterWheelScroll();
    refreshProjectsFilterSelects();
}

window.initProjectsPage = initProjectsPage;
window.showNewProjectModal = showNewProjectModal;
window.showEditProjectModal = showEditProjectModal;
window.showNewProjectModalFromChat = showNewProjectModalFromChat;
window.showNewProjectModalFromChatSidebar = showNewProjectModalFromChatSidebar;
window.saveProjectModal = saveProjectModal;
window.closeProjectModal = closeProjectModal;
window.selectProject = selectProject;
window.switchProjectTab = switchProjectTab;
window.saveProjectSettings = saveProjectSettings;
window.archiveCurrentProject = archiveCurrentProject;
window.deleteCurrentProject = deleteCurrentProject;
window.showProjectListActionMenu = showProjectListActionMenu;
window.editProjectFromListMenu = editProjectFromListMenu;
window.toggleProjectPinnedFromListMenu = toggleProjectPinnedFromListMenu;
window.toggleProjectArchiveFromListMenu = toggleProjectArchiveFromListMenu;
window.deleteProjectFromListMenu = deleteProjectFromListMenu;
window.refreshChatProjectSelector = refreshChatProjectSelector;
window.onChatProjectChange = onChatProjectChange;
window.toggleChatProjectPanel = toggleChatProjectPanel;
window.closeChatProjectPanel = closeChatProjectPanel;
window.selectChatProject = selectChatProject;
window.renderProjectPickerPanel = renderProjectPickerPanel;
window.initProjectPickerPanelSearch = initProjectPickerPanelSearch;
window.clearProjectPickerPanelSearch = clearProjectPickerPanelSearch;
window.scheduleProjectPickerPanelSearch = scheduleProjectPickerPanelSearch;
window.loadChatProjectPanelList = loadChatProjectPanelList;
window.refreshChatProjectFolders = refreshChatProjectFolders;
window.handleProjectFolderSearch = handleProjectFolderSearch;
window.clearProjectFolderSearch = clearProjectFolderSearch;
window.loadMoreChatProjectFolders = loadMoreChatProjectFolders;
window.updateProjectFolderTaskStatuses = updateProjectFolderTaskStatuses;
window.markCurrentProjectConversationViewed = markCurrentProjectConversationViewed;
window.prefetchProjectsForChat = prefetchProjectsForChat;
window.ensureDefaultActiveProjectForNewChat = ensureDefaultActiveProjectForNewChat;
window.getActiveProjectId = getActiveProjectId;
window.getProjectName = getProjectName;
window.openVulnerabilitiesForProject = openVulnerabilitiesForProject;
window.openVulnerabilityDetail = openVulnerabilityDetail;
window.filterProjectsList = filterProjectsList;
window.goProjectsPage = goProjectsPage;
window.changeProjectsPageSize = changeProjectsPageSize;
window.parseProjectsListResponse = parseProjectsListResponse;
window.fetchAllProjects = fetchAllProjects;
window.debouncedLoadProjectVulnerabilities = debouncedLoadProjectVulnerabilities;
window.loadProjectVulnerabilities = loadProjectVulnerabilities;
window.openProjectConversation = openProjectConversation;
window.unbindConversationFromProject = unbindConversationFromProject;
window.loadProjectConversations = loadProjectConversations;
window.promoteConversationAttackChain = promoteConversationAttackChain;
window.rebuildProjectNameMap = rebuildProjectNameMap;
window.rememberProjectsInNameMap = rememberProjectsInNameMap;
window.searchActiveProjects = searchActiveProjects;
window.filterActiveProjectsLocal = filterActiveProjectsLocal;
window.fetchProjectSummary = fetchProjectSummary;
window.projectNameById = projectNameById;
window.ensureProjectsLoaded = ensureProjectsLoaded;
window.isProjectsCacheReady = isProjectsCacheReady;
