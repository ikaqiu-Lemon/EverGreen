import {openEvergreenClaims} from "./evergreenClaims";
import {fetchSyncPost} from "../../util/fetch";
import {escapeHtml} from "../../util/escape";
import {showMessage} from "../../dialog/message";
import {openFileById} from "../../editor/util";
import {openMobileFileById} from "../../mobile/editor";
import {isMobile} from "../../util/functions";

const versions = new WeakMap<IProtyle, number>();

export const openClaimsForProtyle = (protyle: IProtyle, logicalID?: string) =>
    openEvergreenClaims({
        notebookID: protyle.notebookId, logicalID, readOnly: protyle.disabled,
        openDocument: id => {
            if (isMobile()) {
                openMobileFileById(protyle.app, id);
            } else {
                void openFileById({app: protyle.app, id});
            }
        },
    }).catch(error => showMessage(escapeHtml(String(error)), 7000, "error"));

// 普通文档不显示受管编辑入口；异步响应只绑定到请求时的文档。
export const syncEvergreenClaim = async (protyle: IProtyle) => {
    const plugin = protyle.breadcrumb?.element.parentElement?.querySelector<HTMLElement>(".protyle-breadcrumb__plugin");
    const documentID = protyle.block?.rootID;
    const version = (versions.get(protyle) || 0) + 1;
    versions.set(protyle, version);
    plugin?.querySelector("[data-type=evergreen-claim]")?.remove();
    if (!plugin || !documentID) {
        return;
    }
    try {
        const response = await fetchSyncPost("/api/evergreen/v1/claims/get", {document_id: documentID}, undefined, false);
        if (!("ok" in response) || !response.ok || versions.get(protyle) !== version ||
            protyle.block.rootID !== documentID) {
            return;
        }
        const button = document.createElement("button");
        button.type = "button";
        button.className = "block__icon fn__flex-center ariaLabel";
        button.dataset.type = "evergreen-claim";
        button.setAttribute("aria-label", window.siyuan.languages.evergreenClaims);
        button.innerHTML = '<svg><use xlink:href="#iconDatabase"></use></svg>';
        button.addEventListener("click", event => {
            event.stopPropagation();
            void openClaimsForProtyle(protyle, response.data.logical_id);
        });
        plugin.prepend(button);
    } catch {
        // 普通文档或暂时无法读取的文档不安装受管入口。
    }
};
