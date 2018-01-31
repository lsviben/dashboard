import {Component, Input, OnInit, Output} from '@angular/core';
import {ApiService} from 'app/core/services/api/api.service';
import {NodeEntity} from 'app/shared/entity/NodeEntity';
import {CustomEventService} from 'app/core/services';
import { NotificationActions } from 'app/redux/actions/notification.actions';

@Component({
  selector: 'kubermatic-node-delete-confirmation',
  templateUrl: './node-delete-confirmation.component.html',
  styleUrls: ['./node-delete-confirmation.component.scss']
})

export class NodeDeleteConfirmationComponent implements OnInit {

  @Input() nodeUID: string;
  @Input() nodeName: string;
  @Input() clusterName: string;
  @Input() seedDcName: string;
  @Input() onNodeRemoval;

  public node: NodeEntity;

  public title: string;
  public message: string;
  public titleAlign?: string;
  public messageAlign?: string;
  public btnOkText?: string;
  public btnCancelText?: string;


  constructor(
    private api: ApiService,
    private customEventService: CustomEventService) {}

  ngOnInit() {
  }

  public deleteNode(): void {
    this.onNodeRemoval(true);
    this.api.deleteClusterNode(this.clusterName, this.nodeName).subscribe(result => {
      NotificationActions.success('Success', `Node removed successfully`);
      this.customEventService.publish('onNodeDelete', this.nodeName);
      this.onNodeRemoval(false);
    });
  }
}
